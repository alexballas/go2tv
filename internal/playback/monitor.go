package playback

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"
)

var errChromecastStartupIdle = errors.New("chromecast playback did not start")

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

type MonitorEvent struct {
	Generation      uint64
	TimerEpoch      uint64
	State           string
	Position        int
	Duration        int
	ImageReady      bool
	NextURI         string
	NextURIObserved bool
	Terminal        TerminalReason
	Err             error
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
}

// DLNAStopGate suppresses the renderer's initial STOPPED callback. Keeping it
// on the transport preserves completion detection when a monitor restarts.
type DLNAStopGate struct{ armed atomic.Bool }

func (g *DLNAStopGate) Arm()   { g.armed.Store(true) }
func (g *DLNAStopGate) Reset() { g.armed.Store(false) }
func (g *DLNAStopGate) acceptStop() bool {
	return g.armed.Swap(true)
}

func normalizeMonitorConfig(cfg MonitorConfig) MonitorConfig {
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	return cfg
}

func RunDLNAMonitor(ctx context.Context, cfg MonitorConfig, transport DLNATransport, callbacks <-chan DLNACallbackEvent) {
	cfg = normalizeMonitorConfig(cfg)
	ticker := cfg.Clock.NewTicker(time.Second)
	defer ticker.Stop()
	stopGate := cfg.DLNAStopGate
	if stopGate == nil {
		stopGate = &DLNAStopGate{}
	}
	// gaplessStopped records a track-boundary STOPPED that was deferred while a
	// gapless next was staged. Compatible renderers promote the queued item
	// without a genuine stop, so the terminal is withheld until the NextURI poll
	// observes the promotion. If gapless is disengaged without one, the deferred
	// stop is surfaced as end-of-media.
	gaplessStopped := false
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
				gaplessStopped = false
				emitMonitor(ctx, cfg, MonitorEvent{State: "PLAYING"})
			case "PAUSED_PLAYBACK":
				emitMonitor(ctx, cfg, MonitorEvent{State: "PAUSED"})
			case "STOPPED":
				if cfg.ExplicitStop != nil && cfg.ExplicitStop() {
					return
				}
				if !stopGate.acceptStop() {
					continue
				}
				if cfg.GaplessActive != nil && cfg.GaplessActive() {
					// The renderer reports STOPPED as it promotes the staged
					// gapless next. Defer completion and keep polling so the
					// NextURI observation drives the seamless transition.
					gaplessStopped = true
					continue
				}
				emitMonitor(ctx, cfg, MonitorEvent{Terminal: TerminalFinished})
				return
			}
		case <-ticker.C():
			event := MonitorEvent{}
			position, err := transport.Position(ctx)
			if err == nil {
				if position.Current > 0 || position.Duration > 0 {
					stopGate.Arm()
					gaplessStopped = false
				}
				position.Current += cfg.SeekOffset
				if cfg.ExpectedDuration > 0 {
					position.Duration = cfg.ExpectedDuration
				}
				event.Position = position.Current
				event.Duration = position.Duration
			}
			if cfg.GaplessActive != nil && cfg.GaplessActive() {
				if gapless, ok := transport.(DLNAGaplessTransport); ok {
					if nextURI, nextErr := gapless.NextURI(ctx); nextErr == nil {
						event.NextURI = nextURI
						event.NextURIObserved = true
						if nextURI == "" {
							gaplessStopped = false
						}
					}
				}
			} else if gaplessStopped {
				// A deferred boundary STOPPED with gapless now disengaged and no
				// promotion observed means the media genuinely ended.
				emitMonitor(ctx, cfg, MonitorEvent{Terminal: TerminalFinished})
				return
			}
			if err != nil && !event.NextURIObserved {
				continue
			}
			emitMonitor(ctx, cfg, event)
		}
	}
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
