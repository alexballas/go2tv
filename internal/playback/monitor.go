package playback

import (
	"context"
	"time"
)

type terminalReasonKey struct{}

func WithTerminalReason(ctx context.Context, reason TerminalReason) context.Context {
	return context.WithValue(ctx, terminalReasonKey{}, reason)
}

type TerminalReason string

const (
	TerminalFinished    TerminalReason = "finished"
	TerminalUserStop    TerminalReason = "user_stop"
	TerminalReplacement TerminalReason = "replacement"
	TerminalError       TerminalReason = "error"
	TerminalShutdown    TerminalReason = "shutdown"
)

type MonitorEvent struct {
	Generation uint64
	State      string
	Position   int
	Duration   int
	Terminal   TerminalReason
	Err        error
}

type DLNACallbackEvent struct {
	Generation     uint64
	TransportState string
}

type MonitorSink interface {
	HandleMonitorEvent(context.Context, MonitorEvent)
}
type MonitorSinkFunc func(context.Context, MonitorEvent)

func (f MonitorSinkFunc) HandleMonitorEvent(ctx context.Context, e MonitorEvent) { f(ctx, e) }

type MonitorConfig struct {
	Generation   uint64
	SeekOffset   int
	Clock        Clock
	Sink         MonitorSink
	ExplicitStop func() bool
	ErrorLimit   int
}

func normalizeMonitorConfig(cfg MonitorConfig) MonitorConfig {
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.ErrorLimit <= 0 {
		cfg.ErrorLimit = 3
	}
	return cfg
}

func RunDLNAMonitor(ctx context.Context, cfg MonitorConfig, transport DLNATransport, callbacks <-chan DLNACallbackEvent) {
	cfg = normalizeMonitorConfig(cfg)
	ticker := cfg.Clock.NewTicker(time.Second)
	defer ticker.Stop()
	errorsSeen := 0
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
				emitMonitor(ctx, cfg, MonitorEvent{State: "PLAYING"})
			case "PAUSED_PLAYBACK":
				emitMonitor(ctx, cfg, MonitorEvent{State: "PAUSED"})
			case "STOPPED":
				if cfg.ExplicitStop != nil && cfg.ExplicitStop() {
					return
				}
				emitMonitor(ctx, cfg, MonitorEvent{Terminal: TerminalFinished})
				return
			}
		case <-ticker.C():
			position, err := transport.Position(ctx)
			if err != nil {
				errorsSeen++
				if errorsSeen >= cfg.ErrorLimit {
					emitMonitor(ctx, cfg, MonitorEvent{Terminal: TerminalError, Err: err})
					return
				}
				continue
			}
			errorsSeen = 0
			position.Current += cfg.SeekOffset
			emitMonitor(ctx, cfg, MonitorEvent{Position: position.Current, Duration: position.Duration})
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
	duration := 0
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
					if started && last >= 0 && duration > 0 && last >= duration-cfg.StallWindow {
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
					emitMonitor(ctx, cfg.MonitorConfig, MonitorEvent{Terminal: TerminalError})
					return
				}
			}
			current := status.Current + cfg.SeekOffset
			if status.Duration > 0 {
				duration = status.Duration + cfg.SeekOffset
			}
			emitMonitor(ctx, cfg.MonitorConfig, MonitorEvent{State: status.PlayerState, Position: current, Duration: duration})
			if !started || status.PlayerState == "PAUSED" {
				stalled = 0
				continue
			}
			if current != last {
				last, stalled = current, 0
				continue
			}
			if duration <= 0 || last < duration-cfg.StallWindow {
				continue
			}
			stalled++
			threshold := cfg.OtherStallTicks
			if status.PlayerState == "PLAYING" {
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
	cfg.Sink.HandleMonitorEvent(ctx, event)
}

func terminalFromContext(ctx context.Context) TerminalReason {
	if reason, ok := ctx.Value(terminalReasonKey{}).(TerminalReason); ok && reason != "" {
		return reason
	}
	if ctx.Err() != nil {
		return TerminalShutdown
	}
	return TerminalError
}
