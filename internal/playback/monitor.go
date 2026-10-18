package playback

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"
)

var (
	errChromecastStartupIdle = errors.New("chromecast playback did not start")
	errDLNATailGuard         = errors.New("position request withheld near track end")
)

const (
	chromecastImageMetadataTicks = 2
	chromecastImageFallbackTicks = 8
)

type TerminalReason string

const (
	TerminalFinished    TerminalReason = "finished"
	TerminalUserStop    TerminalReason = "user_stop"
	TerminalReplacement TerminalReason = "replacement"
	TerminalError       TerminalReason = "error"
	TerminalShutdown    TerminalReason = "shutdown"
)

func (r TerminalReason) Error() string { return string(r) }

// Recovery reasons other packages act on; compare these, never the text.
const (
	RecoveryStoppedWithoutCallback = "transport stopped without completion callback"
	RecoveryStalledNearEnd         = "position stalled near end"
	RecoveryGaplessHandoffFailed   = "gapless handoff did not start"
	// Consecutive failed handoffs before a renderer is treated as gapless
	// incapable; one slow transition must not disable gapless for good.
	GaplessHandoffStrikes = 2
)

type MonitorEvent struct {
	Generation uint64
	TimerEpoch uint64
	State      string
	Position   int
	Duration   int
	// PositionValid marks a current-track timeline: a sample with a duration,
	// or a confirmed live stream (Duration 0). Transient zero-duration samples,
	// state-only notifications and failed requests leave it false.
	PositionValid   bool
	ImageReady      bool
	NextURI         string
	NextURIObserved bool
	GaplessPromoted bool
	GaplessURI      string
	Recovery        string
	// DLNAStopped is proof from a successful transport-state poll. Cleanup can
	// release subscriptions without sending another remote Stop.
	DLNAStopped bool
	Terminal    TerminalReason
	Err         error
}

type DLNACallbackEvent struct {
	Generation     uint64
	TransportState string
}

type MonitorSink interface {
	HandleMonitorEvent(context.Context, MonitorEvent)
}

type MonitorConfig struct {
	Generation       uint64
	TimerEpoch       uint64
	SeekOffset       int
	ExpectedDuration int
	Clock            Clock
	Sink             MonitorSink
	ExplicitStop     func() bool
	DLNAStopGate     *DLNAStopGate
	GaplessActive    func() bool
	QueuedURI        func() string
	CurrentURI       string
	DLNATimeline     func() (seekOffset, expectedDuration int)
	// Position known when a monitor restarts after a seek or pause, so the
	// tail guard covers its first poll. Ignored unless InitialDuration > 0.
	InitialPosition, InitialDuration int
	// Temporary diagnostics; nil keeps monitoring silent.
}

// DLNAStopGate suppresses the renderer's initial STOPPED callback. Keeping it
// on the transport preserves completion detection when a monitor restarts.
type DLNAStopGate struct {
	armed   atomic.Bool
	started atomic.Bool
}

func (g *DLNAStopGate) Arm()   { g.started.Store(true); g.armed.Store(true) }
func (g *DLNAStopGate) Reset() { g.started.Store(false); g.armed.Store(false) }
func (g *DLNAStopGate) acceptStop() bool {
	return g.armed.Swap(true)
}

// clockNow matches poll timestamps: injected clocks may provide Now.
func clockNow(c Clock) time.Time {
	if clock, ok := c.(interface{ Now() time.Time }); ok {
		return clock.Now()
	}
	return time.Now()
}

func normalizeMonitorConfig(cfg MonitorConfig) MonitorConfig {
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	return cfg
}

const (
	DLNAPollTimeout = 1500 * time.Millisecond
	dlnaStallWindow = 2
	dlnaStallDelay  = 3 * time.Second
	// Samsung reports STOPPED for ~1.2s between gapless tracks.
	dlnaGaplessGrace = 3 * time.Second
	// A renderer already reporting the queued URI is mid-handoff; wait longer
	// before loading it ourselves on top of the renderer's own transition.
	dlnaGaplessHandoffTimeout = 10 * time.Second
	dlnaStartupTimeout        = 30 * time.Second
	dlnaStartupGrace          = 4 * time.Second
	dlnaStartupStoppedDelay   = 3 * time.Second
	dlnaStartupRetries        = 2
	dlnaStartupRetryTimeout   = 5 * time.Second
	// Samsung renderers stop serving UPnP for ~10s when GetPositionInfo arrives
	// near the last frame; GetTransportInfo stays safe. Positions are floored
	// seconds, so a 3s guard keeps every position request >2s before the end.
	dlnaTailGuard = 3 * time.Second
	// Resume asking once the estimate overshoots, so stuck renderers still hit
	// stall recovery and wrong durations cannot hide real progress.
	dlnaTailGuardOvershoot = dlnaStallDelay
)

func RunDLNAMonitor(ctx context.Context, cfg MonitorConfig, transport DLNAPositionTransport, callbacks <-chan DLNACallbackEvent) {
	cfg = normalizeMonitorConfig(cfg)
	ticker := cfg.Clock.NewTicker(time.Second)
	defer ticker.Stop()
	stopGate := cfg.DLNAStopGate
	if stopGate == nil {
		stopGate = &DLNAStopGate{}
	}
	state, currentURI := "", cfg.CurrentURI
	lastPosition, lastDuration := -1, 0
	var lastProgress, boundaryStop, lastSample time.Time
	if cfg.InitialDuration > 0 && cfg.InitialPosition >= 0 {
		lastPosition, lastDuration, lastSample = cfg.InitialPosition, cfg.InitialDuration, clockNow(cfg.Clock)
	}
	startup := dlnaStartup{}
	var live dlnaLiveTimeline
	// stateErr is the latest transport-state result; nil when no fresh poll ran.
	// callbackStop records an accepted STOPPED callback awaiting confirmation.
	// stateLostAt is when that confirmation first failed during gapless.
	var stateErr error
	var stateLostAt time.Time
	callbackStop := false
	finish := func(event MonitorEvent) {
		if event.Terminal == "" {
			event.Terminal = TerminalFinished
		}
		// A stop the renderer announced by callback is a normal end of track.
		if callbackStop && event.Recovery == RecoveryStoppedWithoutCallback {
			event.Recovery = ""
		}
		emitMonitor(ctx, cfg, event)
	}
	poll := func(now time.Time, trigger string) bool {
		if _, ok := cfg.Clock.(interface{ Now() time.Time }); ok || now.IsZero() {
			now = clockNow(cfg.Clock)
		}
		if startup.since.IsZero() {
			startup.since = now
		}
		release := func() {}
		if guard, ok := transport.(DLNAMonitorGuard); ok {
			var acquired bool
			release, acquired = guard.TryMonitorLock()
			if !acquired {
				stateErr = nil
				lastProgress = now
				if result := startup.check(now, state, false, "", currentURI, stopGate.started.Load(), false); result.failure != nil {
					finish(*result.failure)
					return true
				}
				return false
			}
		}
		event, terminal := pollDLNA(ctx, cfg, transport, now, &state, &currentURI, &lastPosition, &lastDuration, &lastProgress, &boundaryStop, &lastSample, &stateErr, stopGate, &startup, &live)
		if ctx.Err() != nil {
			release()
			return false
		}
		if terminal {
			release()
			finish(event)
			return true
		}
		if event.State != "" || event.PositionValid || event.Duration != 0 || event.Position != 0 || event.NextURIObserved || event.GaplessPromoted {
			emitMonitor(ctx, cfg, event)
		}
		release()
		// Playback resumed (a poll confirmed it or a handoff was promoted), so an
		// earlier STOPPED callback no longer describes the renderer.
		if state == "PLAYING" || state == "PAUSED_PLAYBACK" {
			callbackStop = false
		}
		if stateErr == nil {
			stateLostAt = time.Time{}
		}
		// A renderer that answers but cannot report its state must not strand a
		// callback-announced stop; only a timeout (frozen renderer) keeps waiting.
		// Without state a gapless handoff is unverifiable: count it as failed.
		if callbackStop && stateErr != nil && !errors.Is(stateErr, context.DeadlineExceeded) && stopGate.started.Load() {
			trusted := MonitorEvent{}
			if cfg.GaplessActive != nil && cfg.GaplessActive() {
				// Renderers may fault while switching tracks; give the handoff
				// the normal gapless grace before counting it as failed.
				if stateLostAt.IsZero() {
					stateLostAt = now
				}
				if now.Sub(stateLostAt) < dlnaGaplessGrace {
					return false
				}
				trusted.Recovery = RecoveryGaplessHandoffFailed
			}
			finish(trusted)
			return true
		}
		return false
	}
	for {
		select {
		case <-ctx.Done():
			emitMonitor(ctx, cfg, MonitorEvent{Terminal: terminalFromContext(ctx)})
			return
		case event, ok := <-callbacks:
			if !ok {
				callbacks = nil
				continue
			}
			if event.Generation != cfg.Generation {
				continue
			}
			switch event.TransportState {
			case "PLAYING":
				stopGate.Arm()
				callbackStop = false
				if state != "PLAYING" {
					lastProgress = time.Time{}
				}
				state = "PLAYING"
				emitMonitor(ctx, cfg, MonitorEvent{State: "PLAYING"})
			case "PAUSED_PLAYBACK":
				callbackStop = false
				state, lastProgress, boundaryStop = "PAUSED_PLAYBACK", time.Time{}, time.Time{}
				emitMonitor(ctx, cfg, MonitorEvent{State: "PAUSED"})
			case "TRANSITIONING":
				startup.transitioning = true
			case "STOPPED":
				if cfg.ExplicitStop != nil && cfg.ExplicitStop() {
					return
				}
				if !stopGate.acceptStop() {
					continue
				}
				callbackStop = true
				if _, ok := transport.(DLNAStatusTransport); ok {
					state = "STOPPED"
					if poll(clockNow(cfg.Clock), "STOPPED callback") {
						return
					}
					continue
				}
				if cfg.GaplessActive != nil && cfg.GaplessActive() {
					state = "STOPPED"
					continue
				}
				finish(MonitorEvent{})
				return
			}
		case now := <-ticker.C():
			if poll(now, "periodic poll") {
				return
			}
		}
	}
}

func pollDLNA(ctx context.Context, cfg MonitorConfig, transport DLNAPositionTransport, now time.Time, state, currentURI *string, lastPosition, lastDuration *int, lastProgress, boundaryStop, lastSample *time.Time, stateErr *error, stopGate *DLNAStopGate, startup *dlnaStartup, live *dlnaLiveTimeline) (MonitorEvent, bool) {
	event := MonitorEvent{}
	// State gets first chance even when position requests hang at EOF. Each
	// SOAP operation has its own deadline; one timeout cannot starve the next.
	typed, hasStatus := transport.(DLNAStatusTransport)
	stateConfirmed := false
	if hasStatus {
		requestCtx, cancel := context.WithTimeout(ctx, DLNAPollTimeout)
		polled, err := typed.TransportState(requestCtx)
		cancel()
		*stateErr = err
		if err == nil {
			stateConfirmed = true
			polled = strings.ToUpper(strings.TrimSpace(polled))
			if polled != *state {
				*lastProgress = now
			}
			*state = polled
			switch polled {
			case "PLAYING":
				stopGate.Arm()
				event.State = "PLAYING"
			case "PAUSED_PLAYBACK":
				event.State = "PAUSED"
				*lastProgress, *boundaryStop = now, time.Time{}
			}
		}
	}
	gapless := cfg.GaplessActive != nil && cfg.GaplessActive()
	queuedURI := ""
	if cfg.QueuedURI != nil {
		queuedURI = cfg.QueuedURI()
	}
	if stateConfirmed && *state == "STOPPED" && stopGate.started.Load() {
		// The track that just ended is never a handoff target (one-item loops share URLs).
		handoff := gapless && queuedURI != *currentURI && dlnaHandoffObserved(ctx, transport, queuedURI)
		return dlnaStoppedResult(now, gapless, handoff, boundaryStop, true)
	}
	var position Position
	var positionErr error
	if estimate, guarded := dlnaTailEstimate(now, *state, *lastPosition, *lastDuration, *lastSample); guarded && stopGate.started.Load() {
		positionErr = errDLNATailGuard
		event.Position, event.Duration = estimate, *lastDuration
		event.PositionValid = true
	} else {
		requestCtx, cancel := context.WithTimeout(ctx, DLNAPollTimeout)
		position, positionErr = transport.Position(requestCtx)
		cancel()
	}
	observedURI := position.TrackURI
	if gapless {
		switch typed := transport.(type) {
		case DLNAMediaInfoTransport:
			requestCtx, cancel := context.WithTimeout(ctx, DLNAPollTimeout)
			current, next, err := typed.MediaInfo(requestCtx)
			cancel()
			if err == nil {
				event.NextURI, event.NextURIObserved = next, true
				if observedURI == "" {
					observedURI = current
				}
			}
		case DLNAGaplessTransport:
			requestCtx, cancel := context.WithTimeout(ctx, DLNAPollTimeout)
			next, err := typed.NextURI(requestCtx)
			cancel()
			if err == nil {
				event.NextURI, event.NextURIObserved = next, true
			}
		}
	}
	// An empty NextURI alone also means rejection/clearing on some renderers.
	// Track identity must match the queued resource before old routes retire.
	// Before playback starts, or while the renderer still reports another track,
	// samples describe stale media and must not reach the UI or tail detection.
	trackMatches := sameTrackURI(observedURI, *currentURI)
	if positionErr == nil && trackMatches {
		reportedProgress := position.Current > 0
		offset, duration := cfg.SeekOffset, cfg.ExpectedDuration
		if cfg.DLNATimeline != nil {
			offset, duration = cfg.DLNATimeline()
		}
		position.Current += offset
		if duration > 0 {
			position.Duration = duration
		}
		if reportedProgress {
			stopGate.Arm()
		}
		if stopGate.started.Load() {
			event.Position, event.Duration = position.Current, position.Duration
		}
		if position.Duration > 0 {
			*live = dlnaLiveTimeline{}
			if position.Current != *lastPosition || position.Duration != *lastDuration || lastProgress.IsZero() {
				*lastProgress = now
			}
			// A stationary sample must not re-arm the tail estimate.
			if position.Current != *lastPosition || *state != "PLAYING" {
				*lastSample = now
			}
			*lastPosition, *lastDuration = position.Current, position.Duration
		} else if live.observe(position.Current) {
			// Live streams can briefly retain the previous file's duration.
			// Once it disappears, retire its tail/stall estimate as well.
			*lastPosition, *lastDuration = -1, 0
			*lastSample = time.Time{}
		}
		if stopGate.started.Load() && (position.Duration > 0 || live.active) {
			event.PositionValid = true
		}
	}
	retrier, retryable := transport.(DLNAStartupRetryTransport)
	switch result := startup.check(now, *state, stateConfirmed, observedURI, *currentURI, stopGate.started.Load(), retryable); {
	case result.failure != nil:
		return *result.failure, true
	case result.retry:
		requestCtx, cancel := context.WithTimeout(ctx, dlnaStartupRetryTimeout)
		// A failed retry is judged again by the next poll's startup check.
		_ = retrier.RetryStart(requestCtx, *currentURI, result.reload)
		cancel()
	}
	if positionErr != nil && !errors.Is(positionErr, errDLNATailGuard) {
		*lastProgress = now
	}
	if hasStatus && !stateConfirmed {
		*lastProgress, *boundaryStop = now, time.Time{}
	}
	promoted := stateConfirmed && *state == "PLAYING" && gapless && sameKnownURI(observedURI, queuedURI) &&
		queuedURI != *currentURI
	if promoted {
		event.GaplessPromoted = true
		event.GaplessURI = queuedURI
		event.State = "PLAYING"
		*state, *currentURI = "PLAYING", queuedURI
		*lastPosition, *lastDuration = -1, 0
		*live = dlnaLiveTimeline{}
		*lastProgress, *boundaryStop = now, time.Time{}
		return event, false
	}
	nearEnd := *lastDuration > 0 && *lastPosition >= *lastDuration-dlnaStallWindow
	if *state == "STOPPED" && stopGate.started.Load() && (stateConfirmed || !hasStatus) {
		handoff := queuedURI != *currentURI && sameKnownURI(observedURI, queuedURI)
		if stoppedEvent, terminal := dlnaStoppedResult(now, gapless, handoff, boundaryStop, stateConfirmed); terminal {
			return stoppedEvent, true
		}
	} else if *state != "STOPPED" {
		*boundaryStop = time.Time{}
	}
	if stateConfirmed && *state == "PLAYING" && positionErr == nil && nearEnd && !lastProgress.IsZero() && now.Sub(*lastProgress) >= dlnaStallDelay {
		return MonitorEvent{Recovery: RecoveryStalledNearEnd}, true
	}
	return event, false
}

// dlnaLiveTimeline separates live streams from renderers that briefly or
// permanently report an empty timeline (seek transitions, stuck EOF): only
// zero-duration samples whose position advances confirm a live stream.
type dlnaLiveTimeline struct {
	position     int
	seen, active bool
}

// observe records a zero-duration sample and reports when it confirms a
// live stream for the first time.
func (l *dlnaLiveTimeline) observe(position int) bool {
	confirmed := !l.active && l.seen && position > l.position
	l.active = l.active || confirmed
	l.position, l.seen = position, true
	return confirmed
}

// dlnaTailEstimate extrapolates a playing track's position from the last
// renderer sample while it is within the guard window around the end.
func dlnaTailEstimate(now time.Time, state string, position, duration int, sampledAt time.Time) (int, bool) {
	if state != "PLAYING" || position < 0 || duration <= 0 || sampledAt.IsZero() {
		return 0, false
	}
	estimate := time.Duration(position)*time.Second + now.Sub(sampledAt)
	end := time.Duration(duration) * time.Second
	if estimate < end-dlnaTailGuard || estimate >= end+dlnaTailGuardOvershoot {
		return 0, false
	}
	return min(int(estimate/time.Second), duration), true
}

// dlnaHandoffObserved reports whether a STOPPED renderer already took the
// queued URI. GetMediaInfo stays responsive at EOF, unlike GetPositionInfo.
func dlnaHandoffObserved(ctx context.Context, transport DLNAPositionTransport, queuedURI string) bool {
	typed, ok := transport.(DLNAMediaInfoTransport)
	if !ok || queuedURI == "" {
		return false
	}
	requestCtx, cancel := context.WithTimeout(ctx, DLNAPollTimeout)
	current, _, err := typed.MediaInfo(requestCtx)
	cancel()
	return err == nil && sameKnownURI(current, queuedURI)
}

func dlnaStoppedResult(now time.Time, gapless, handoff bool, boundaryStop *time.Time, verified bool) (MonitorEvent, bool) {
	if !gapless {
		return MonitorEvent{Recovery: RecoveryStoppedWithoutCallback, DLNAStopped: verified}, true
	}
	if boundaryStop.IsZero() {
		*boundaryStop = now
	}
	limit := dlnaGaplessGrace
	if handoff {
		limit = dlnaGaplessHandoffTimeout
	}
	if now.Sub(*boundaryStop) >= limit {
		return MonitorEvent{Recovery: RecoveryGaplessHandoffFailed, DLNAStopped: verified}, true
	}
	return MonitorEvent{State: "STOPPED"}, false
}

type ChromecastMonitorConfig struct {
	MonitorConfig
	StartupIdleTicks  int
	LostPolls         int
	StallWindow       int
	PlayingStallTicks int
	OtherStallTicks   int
}

func RunChromecastMonitor(ctx context.Context, cfg ChromecastMonitorConfig, transport ChromecastTransport) {
	cfg.MonitorConfig = normalizeMonitorConfig(cfg.MonitorConfig)
	if cfg.StartupIdleTicks <= 0 {
		cfg.StartupIdleTicks = 20
	}
	if cfg.LostPolls <= 0 {
		cfg.LostPolls = 3
	}
	if cfg.StallWindow <= 0 {
		cfg.StallWindow = 5
	}
	if cfg.PlayingStallTicks <= 0 {
		cfg.PlayingStallTicks = 3
	}
	if cfg.OtherStallTicks <= 0 {
		cfg.OtherStallTicks = 10
	}
	ticker := cfg.Clock.NewTicker(time.Second)
	defer ticker.Stop()
	started, last := false, -1
	idle, lost, stalled := 0, 0, 0
	lastDuration := 0
	imageReady, imageMetadataTicks, imageFallbackTicks := false, 0, 0
	for {
		select {
		case <-ctx.Done():
			emitMonitor(ctx, cfg.MonitorConfig, MonitorEvent{Terminal: terminalFromContext(ctx)})
			return
		case <-ticker.C():
			status, err := transport.Status(ctx)
			if err != nil {
				lost++
				if lost >= cfg.LostPolls {
					reason := TerminalError
					if started && last >= 0 && lastDuration > 0 && last >= lastDuration-cfg.StallWindow {
						reason = TerminalFinished
					}
					emitMonitor(ctx, cfg.MonitorConfig, MonitorEvent{Terminal: reason, Err: err})
					return
				}
				continue
			}
			lost = 0
			switch status.PlayerState {
			case "BUFFERING":
				idle = 0
			case "PLAYING", "PAUSED":
				started, idle = true, 0
			case "IDLE":
				if started {
					emitMonitor(ctx, cfg.MonitorConfig, MonitorEvent{Terminal: TerminalFinished})
					return
				}
				idle++
				if idle >= cfg.StartupIdleTicks {
					emitMonitor(ctx, cfg.MonitorConfig, MonitorEvent{Terminal: TerminalFinished, Err: errChromecastStartupIdle})
					return
				}
			}
			current := status.Current + cfg.SeekOffset
			duration := status.Duration
			if cfg.ExpectedDuration > 0 {
				duration = cfg.ExpectedDuration
			}
			sampleValid := status.PlayerState != "BUFFERING" && duration > 0
			if !imageReady {
				imageFallbackTicks++
				metadataReady := strings.HasPrefix(strings.ToLower(strings.TrimSpace(status.ContentType)), "image/") || strings.TrimSpace(status.MediaTitle) != ""
				switch {
				case status.PlayerState == "PLAYING" || status.PlayerState == "PAUSED":
					imageReady = true
				case metadataReady && status.PlayerState != "BUFFERING":
					imageReady = true
				case metadataReady:
					imageMetadataTicks++
					imageReady = imageMetadataTicks >= chromecastImageMetadataTicks
				default:
					imageMetadataTicks = 0
				}
				if imageFallbackTicks >= chromecastImageFallbackTicks {
					imageReady = true
				}
			}
			event := MonitorEvent{State: status.PlayerState, ImageReady: imageReady}
			if sampleValid {
				event.Position = current
				event.Duration = duration
			}
			emitMonitor(ctx, cfg.MonitorConfig, event)
			if !started || status.PlayerState == "PAUSED" {
				stalled = 0
				continue
			}
			if sampleValid && current != last {
				last, lastDuration, stalled = current, duration, 0
				continue
			}
			if lastDuration <= 0 || last < lastDuration-cfg.StallWindow {
				continue
			}
			stalled++
			threshold := cfg.OtherStallTicks
			if sampleValid && status.PlayerState == "PLAYING" {
				threshold = cfg.PlayingStallTicks
			}
			if stalled >= threshold {
				emitMonitor(ctx, cfg.MonitorConfig, MonitorEvent{Terminal: TerminalFinished})
				return
			}
		}
	}
}

func RunImageTimer(ctx context.Context, cfg MonitorConfig, delay time.Duration) {
	cfg = normalizeMonitorConfig(cfg)
	timer := cfg.Clock.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C():
		emitMonitor(ctx, cfg, MonitorEvent{Terminal: TerminalFinished})
	}
}

func ShouldAdvance(reason TerminalReason, loop, autoplay bool) bool {
	return reason == TerminalFinished && (loop || autoplay)
}

func emitMonitor(ctx context.Context, cfg MonitorConfig, event MonitorEvent) {
	if cfg.Sink == nil {
		return
	}
	event.Generation = cfg.Generation
	event.TimerEpoch = cfg.TimerEpoch
	cfg.Sink.HandleMonitorEvent(ctx, event)
}

func terminalFromContext(ctx context.Context) TerminalReason {
	var reason TerminalReason
	if errors.As(context.Cause(ctx), &reason) && reason != "" {
		return reason
	}
	if ctx.Err() != nil {
		return TerminalShutdown
	}
	return TerminalError
}
