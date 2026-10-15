package playback

import (
	"context"
	"errors"
	"fmt"
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
	Generation      uint64
	TimerEpoch      uint64
	State           string
	Position        int
	Duration        int
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
	DLNADebug func(string, ...any)
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
	trace := &dlnaDebug{write: cfg.DLNADebug}
	trace.log("monitor_started", "generation", cfg.Generation, "current_uri", cfg.CurrentURI, "poll_timeout_ms", DLNAPollTimeout.Milliseconds(), "poll_deadline_scope", "per operation", "poll_order", "transport_state, position, media_info", "stall_window_seconds", dlnaStallWindow, "stall_threshold_ms", dlnaStallDelay.Milliseconds(), "gapless_grace_ms", dlnaGaplessGrace.Milliseconds(), "gapless_handoff_timeout_ms", dlnaGaplessHandoffTimeout.Milliseconds(), "startup_timeout_ms", dlnaStartupTimeout.Milliseconds(), "startup_grace_ms", dlnaStartupGrace.Milliseconds(), "startup_stopped_ms", dlnaStartupStoppedDelay.Milliseconds())
	state, currentURI := "", cfg.CurrentURI
	lastPosition, lastDuration := -1, 0
	var lastProgress, boundaryStop, lastSample time.Time
	if cfg.InitialDuration > 0 && cfg.InitialPosition >= 0 {
		lastPosition, lastDuration, lastSample = cfg.InitialPosition, cfg.InitialDuration, clockNow(cfg.Clock)
		trace.log("tail_seeded", "position", lastPosition, "duration", lastDuration)
	}
	startup := dlnaStartup{}
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
		trace.log("completion", "terminal", event.Terminal, "reason", event.Recovery, "state", state, "position", lastPosition, "duration", lastDuration, "current_uri", currentURI, "renderer_stopped", event.DLNAStopped, "error", event.Err)
		emitMonitor(ctx, cfg, event)
	}
	poll := func(now time.Time, trigger string) bool {
		trace.trigger = trigger
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
				trace.change("queue_busy", "true", "poll_deferred", "reason", "gapless queue preparation in flight")
				lastProgress = now
				if result := startup.check(now, state, false, "", currentURI, stopGate.started.Load(), false, trace); result.failure != nil {
					finish(*result.failure)
					return true
				}
				return false
			}
		}
		trace.change("queue_busy", "false", "poll_available")
		event, terminal := pollDLNA(ctx, cfg, transport, trace, now, &state, &currentURI, &lastPosition, &lastDuration, &lastProgress, &boundaryStop, &lastSample, &stateErr, stopGate, &startup)
		if ctx.Err() != nil {
			release()
			return false
		}
		if terminal {
			release()
			finish(event)
			return true
		}
		if event.State != "" || event.Duration != 0 || event.Position != 0 || event.NextURIObserved || event.GaplessPromoted {
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
					trace.log("stop_trust_deferred", "reason", "gapless handoff may be in progress", "grace_ms", dlnaGaplessGrace.Milliseconds(), "error", stateErr)
				}
				if now.Sub(stateLostAt) < dlnaGaplessGrace {
					return false
				}
				trusted.Recovery = RecoveryGaplessHandoffFailed
			}
			trace.log("stop_trusted", "reason", "transport state unavailable", "gapless", trusted.Recovery != "", "error", stateErr)
			finish(trusted)
			return true
		}
		return false
	}
	for {
		select {
		case <-ctx.Done():
			trace.log("monitor_stopped", "cause", context.Cause(ctx), "state", state, "position", lastPosition, "duration", lastDuration)
			emitMonitor(ctx, cfg, MonitorEvent{Terminal: terminalFromContext(ctx)})
			return
		case event, ok := <-callbacks:
			trace.trigger = "callback"
			if !ok {
				callbacks = nil
				continue
			}
			if event.Generation != cfg.Generation {
				trace.log("callback_ignored", "reason", "stale generation", "callback_generation", event.Generation, "generation", cfg.Generation, "callback_state", event.TransportState)
				continue
			}
			trace.log("callback", "callback_state", event.TransportState, "previous_state", state, "position", lastPosition, "duration", lastDuration, "playback_started", stopGate.started.Load())
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
					trace.log("callback_ignored", "reason", "explicit Stop")
					return
				}
				if !stopGate.acceptStop() {
					trace.log("callback_ignored", "reason", "initial STOPPED")
					continue
				}
				callbackStop = true
				if _, ok := transport.(DLNAStatusTransport); ok {
					trace.log("stop_verification", "previous_state", state, "position", lastPosition, "duration", lastDuration)
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

func pollDLNA(ctx context.Context, cfg MonitorConfig, transport DLNAPositionTransport, trace *dlnaDebug, now time.Time, state, currentURI *string, lastPosition, lastDuration *int, lastProgress, boundaryStop, lastSample *time.Time, stateErr *error, stopGate *DLNAStopGate, startup *dlnaStartup) (MonitorEvent, bool) {
	event := MonitorEvent{}
	// State gets first chance even when position requests hang at EOF. Each
	// SOAP operation has its own deadline; one timeout cannot starve the next.
	typed, hasStatus := transport.(DLNAStatusTransport)
	stateConfirmed := false
	if hasStatus {
		requestStarted := time.Now()
		requestCtx, cancel := context.WithTimeout(ctx, DLNAPollTimeout)
		polled, err := typed.TransportState(requestCtx)
		cancel()
		trace.result("transport_state", requestStarted, err)
		*stateErr = err
		if trace.trigger == "STOPPED callback" {
			trace.log("stop_verification_result", "state", polled, "state_confirmed", err == nil, "last_position", *lastPosition, "last_duration", *lastDuration, "error", err)
		}
		if err == nil {
			stateConfirmed = true
			polled = strings.ToUpper(strings.TrimSpace(polled))
			trace.change("transport_state", polled, "transport_state", "previous_state", *state, "state", polled, "last_position", *lastPosition, "last_duration", *lastDuration)
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
	trace.change("gapless", fmt.Sprint(gapless, queuedURI), "gapless_status", "active", gapless, "queued_uri", queuedURI)
	if stateConfirmed && *state == "STOPPED" && stopGate.started.Load() {
		trace.change("position_skipped", "stopped", "position_skipped", "reason", "renderer already confirmed STOPPED", "last_position", *lastPosition, "last_duration", *lastDuration)
		// The track that just ended is never a handoff target (one-item loops share URLs).
		handoff := gapless && queuedURI != *currentURI && dlnaHandoffObserved(ctx, transport, queuedURI, trace)
		return dlnaStoppedResult(now, gapless, handoff, boundaryStop, *currentURI, queuedURI, true, trace)
	}
	delete(trace.values, "position_skipped")
	requestStarted := time.Now()
	var position Position
	var positionErr error
	if estimate, guarded := dlnaTailEstimate(now, *state, *lastPosition, *lastDuration, *lastSample); guarded && stopGate.started.Load() {
		trace.change("tail_guard", "true", "position_withheld", "reason", "estimated end of track", "estimate", estimate, "last_position", *lastPosition, "duration", *lastDuration, "sampled_ms_ago", now.Sub(*lastSample).Milliseconds())
		positionErr = errDLNATailGuard
		event.Position, event.Duration = estimate, *lastDuration
	} else {
		delete(trace.values, "tail_guard")
		requestCtx, cancel := context.WithTimeout(ctx, DLNAPollTimeout)
		position, positionErr = transport.Position(requestCtx)
		cancel()
		trace.result("position", requestStarted, positionErr)
	}
	observedURI := position.TrackURI
	if gapless {
		switch typed := transport.(type) {
		case DLNAMediaInfoTransport:
			requestStarted = time.Now()
			requestCtx, cancel := context.WithTimeout(ctx, DLNAPollTimeout)
			current, next, err := typed.MediaInfo(requestCtx)
			cancel()
			trace.result("media_info", requestStarted, err)
			if err == nil {
				event.NextURI, event.NextURIObserved = next, true
				if observedURI == "" {
					observedURI = current
				}
			}
		case DLNAGaplessTransport:
			requestStarted = time.Now()
			requestCtx, cancel := context.WithTimeout(ctx, DLNAPollTimeout)
			next, err := typed.NextURI(requestCtx)
			cancel()
			trace.result("next_uri", requestStarted, err)
			if err == nil {
				event.NextURI, event.NextURIObserved = next, true
			}
		}
	}
	if event.NextURIObserved {
		trace.change("next_uri", event.NextURI, "next_uri_observed", "next_uri", event.NextURI, "observed_uri", observedURI, "expected_uri", queuedURI)
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
			if position.Current != *lastPosition || position.Duration != *lastDuration || lastProgress.IsZero() {
				*lastProgress = now
			}
			// A stationary sample must not re-arm the tail estimate.
			if position.Current != *lastPosition || *state != "PLAYING" {
				*lastSample = now
			}
			*lastPosition, *lastDuration = position.Current, position.Duration
		}
	}
	retrier, retryable := transport.(DLNAStartupRetryTransport)
	switch result := startup.check(now, *state, stateConfirmed, observedURI, *currentURI, stopGate.started.Load(), retryable, trace); {
	case result.failure != nil:
		return *result.failure, true
	case result.retry:
		requestStarted = time.Now()
		requestCtx, cancel := context.WithTimeout(ctx, dlnaStartupRetryTimeout)
		err := retrier.RetryStart(requestCtx, *currentURI, result.reload)
		cancel()
		trace.result("startup_retry", requestStarted, err)
		trace.log("startup_retry_result", "reload", result.reload, "elapsed_ms", time.Since(requestStarted).Milliseconds(), "error", err)
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
		trace.log("gapless_verified", "previous_uri", *currentURI, "current_uri", observedURI, "queued_uri", queuedURI, "next_uri", event.NextURI, "position", position.Current, "duration", position.Duration)
		event.GaplessPromoted = true
		event.GaplessURI = queuedURI
		event.State = "PLAYING"
		*state, *currentURI = "PLAYING", queuedURI
		*lastPosition, *lastDuration = -1, 0
		*lastProgress, *boundaryStop = now, time.Time{}
		return event, false
	}
	nearEnd := *lastDuration > 0 && *lastPosition >= *lastDuration-dlnaStallWindow
	trace.change("observed_uri", observedURI, "track_observed", "current_uri", *currentURI, "observed_uri", observedURI, "queued_uri", queuedURI)
	trace.change("near_end", fmt.Sprint(nearEnd), "tail_status", "near_end", nearEnd, "position", *lastPosition, "duration", *lastDuration, "state", *state)
	if !stopGate.started.Load() || nearEnd || *state == "STOPPED" || gapless && event.NextURIObserved && event.NextURI == "" {
		stalled := int64(0)
		if !lastProgress.IsZero() {
			stalled = now.Sub(*lastProgress).Milliseconds()
		}
		sampleKey := fmt.Sprint(*state, position.Current, position.Duration, positionErr == nil, stateConfirmed, observedURI, queuedURI, event.NextURI, event.NextURIObserved)
		trace.sample(now, sampleKey, "state", *state, "state_confirmed", stateConfirmed, "position", position.Current, "duration", position.Duration, "last_position", *lastPosition, "last_duration", *lastDuration, "stationary_ms", stalled, "position_valid", positionErr == nil, "playback_started", stopGate.started.Load(), "current_uri", *currentURI, "observed_uri", observedURI, "queued_uri", queuedURI, "next_uri", event.NextURI, "next_observed", event.NextURIObserved)
	}
	if *state == "STOPPED" && stopGate.started.Load() && (stateConfirmed || !hasStatus) {
		handoff := queuedURI != *currentURI && sameKnownURI(observedURI, queuedURI)
		if stoppedEvent, terminal := dlnaStoppedResult(now, gapless, handoff, boundaryStop, *currentURI, queuedURI, stateConfirmed, trace); terminal {
			return stoppedEvent, true
		}
	} else if *state != "STOPPED" {
		*boundaryStop = time.Time{}
	}
	if stateConfirmed && *state == "PLAYING" && positionErr == nil && nearEnd && !lastProgress.IsZero() && now.Sub(*lastProgress) >= dlnaStallDelay {
		trace.log("stall_recovery", "stationary_ms", now.Sub(*lastProgress).Milliseconds(), "threshold_ms", dlnaStallDelay.Milliseconds(), "position", *lastPosition, "duration", *lastDuration)
		return MonitorEvent{Recovery: RecoveryStalledNearEnd}, true
	}
	return event, false
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
func dlnaHandoffObserved(ctx context.Context, transport DLNAPositionTransport, queuedURI string, trace *dlnaDebug) bool {
	typed, ok := transport.(DLNAMediaInfoTransport)
	if !ok || queuedURI == "" {
		return false
	}
	requestStarted := time.Now()
	requestCtx, cancel := context.WithTimeout(ctx, DLNAPollTimeout)
	current, _, err := typed.MediaInfo(requestCtx)
	cancel()
	trace.result("media_info", requestStarted, err)
	handoff := err == nil && sameKnownURI(current, queuedURI)
	trace.change("gapless_handoff", fmt.Sprint(handoff), "gapless_handoff_status", "handoff", handoff, "renderer_uri", current, "queued_uri", queuedURI)
	return handoff
}

func dlnaStoppedResult(now time.Time, gapless, handoff bool, boundaryStop *time.Time, currentURI, queuedURI string, verified bool, trace *dlnaDebug) (MonitorEvent, bool) {
	if !gapless {
		return MonitorEvent{Recovery: RecoveryStoppedWithoutCallback, DLNAStopped: verified}, true
	}
	if boundaryStop.IsZero() {
		*boundaryStop = now
		trace.log("gapless_grace_started", "grace_ms", dlnaGaplessGrace.Milliseconds(), "handoff_timeout_ms", dlnaGaplessHandoffTimeout.Milliseconds(), "current_uri", currentURI, "queued_uri", queuedURI)
	}
	limit := dlnaGaplessGrace
	if handoff {
		limit = dlnaGaplessHandoffTimeout
	}
	if now.Sub(*boundaryStop) >= limit {
		trace.log("gapless_fallback", "waited_ms", now.Sub(*boundaryStop).Milliseconds(), "reason", "renderer still STOPPED", "handoff", handoff, "current_uri", currentURI, "queued_uri", queuedURI)
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
