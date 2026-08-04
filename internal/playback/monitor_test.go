package playback

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type manualTicker struct{ ch chan time.Time }

func (t *manualTicker) C() <-chan time.Time { return t.ch }
func (*manualTicker) Stop()                 {}

type manualTimer struct{ ch chan time.Time }

func (t *manualTimer) C() <-chan time.Time { return t.ch }
func (*manualTimer) Stop() bool            { return true }

type manualClock struct {
	tick  *manualTicker
	timer *manualTimer
}

func newManualClock() *manualClock {
	return &manualClock{tick: &manualTicker{make(chan time.Time, 16)}, timer: &manualTimer{make(chan time.Time, 1)}}
}

func (c *manualClock) NewTicker(time.Duration) Ticker {
	return c.tick
}
func (c *manualClock) NewTimer(time.Duration) Timer {
	return c.timer
}

type monitorCollector struct{ ch chan MonitorEvent }

func (c monitorCollector) HandleMonitorEvent(_ context.Context, e MonitorEvent) { c.ch <- e }

type failingPositionDLNA struct {
	seekDLNA
	polled chan struct{}
}

type gaplessMonitorDLNA struct {
	seekDLNA
	nextURI string
	polls   int
}

func (*gaplessMonitorDLNA) SetNext(context.Context, LoadRequest) error { return nil }
func (d *gaplessMonitorDLNA) NextURI(context.Context) (string, error) {
	d.polls++
	return d.nextURI, nil
}
func (*gaplessMonitorDLNA) ClearNext(context.Context) error { return nil }

func (d *failingPositionDLNA) Position(context.Context) (Position, error) {
	d.polled <- struct{}{}
	return Position{}, errors.New("position unavailable")
}

func waitMonitor(t *testing.T, ch <-chan MonitorEvent) MonitorEvent {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(time.Second):
		t.Fatal("monitor timeout")
		return MonitorEvent{}
	}
}

func TestDLNAMonitorProgressCompletionAndCancellation(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 8)
	callbacks := make(chan DLNACallbackEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	d := &seekDLNA{pos: Position{Current: 3, Duration: 10}}
	go RunDLNAMonitor(ctx, MonitorConfig{Generation: 7, SeekOffset: 2, Clock: clock, Sink: monitorCollector{events}}, d, callbacks)
	clock.tick.ch <- time.Time{}
	e := waitMonitor(t, events)
	if e.Position != 5 || e.Duration != 10 {
		t.Fatalf("progress %#v", e)
	}
	callbacks <- DLNACallbackEvent{Generation: 7, TransportState: "PLAYING"}
	e = waitMonitor(t, events)
	if e.State != "PLAYING" {
		t.Fatalf("state %q", e.State)
	}
	callbacks <- DLNACallbackEvent{Generation: 7, TransportState: "STOPPED"}
	e = waitMonitor(t, events)
	if e.Terminal != TerminalFinished {
		t.Fatalf("terminal %q", e.Terminal)
	}
	cancel()

	clock = newManualClock()
	events = make(chan MonitorEvent, 2)
	replacementCtx, cancelReplacement := context.WithCancelCause(context.Background())
	go RunDLNAMonitor(replacementCtx, MonitorConfig{Clock: clock, Sink: monitorCollector{events}}, d, nil)
	cancelReplacement(TerminalReplacement)
	e = waitMonitor(t, events)
	if e.Terminal != TerminalReplacement {
		t.Fatalf("cancel %q", e.Terminal)
	}
}

func TestDLNAMonitorUsesExpectedTranscodeDuration(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 1)
	dlna := &seekDLNA{pos: Position{Current: 3}}
	go RunDLNAMonitor(t.Context(), MonitorConfig{
		ExpectedDuration: 100,
		Clock:            clock,
		Sink:             monitorCollector{events},
	}, dlna, nil)

	clock.tick.ch <- time.Time{}
	event := waitMonitor(t, events)
	if event.Position != 3 || event.Duration != 100 {
		t.Fatalf("progress = %#v", event)
	}
}

func TestDLNAMonitorObservesConsumedNextURIOnlyWhileGaplessActive(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 2)
	dlna := &gaplessMonitorDLNA{seekDLNA: seekDLNA{posErr: errors.New("position unavailable")}}
	var active atomic.Bool
	go RunDLNAMonitor(t.Context(), MonitorConfig{
		Generation: 2, Clock: clock, Sink: monitorCollector{events},
		GaplessActive: active.Load,
	}, dlna, nil)
	clock.tick.ch <- time.Time{}
	select {
	case event := <-events:
		t.Fatalf("inactive gapless observation = %#v", event)
	case <-time.After(10 * time.Millisecond):
	}
	active.Store(true)
	clock.tick.ch <- time.Time{}
	event := waitMonitor(t, events)
	if !event.NextURIObserved || event.NextURI != "" || dlna.polls != 1 {
		t.Fatalf("gapless observation = %#v, polls=%d", event, dlna.polls)
	}
}

func TestDLNAMonitorDefersBoundaryStopWhileGaplessActive(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 4)
	callbacks := make(chan DLNACallbackEvent, 2)
	dlna := &gaplessMonitorDLNA{seekDLNA: seekDLNA{posErr: errors.New("stopped")}, nextURI: "NOT_IMPLEMENTED"}
	var active atomic.Bool
	active.Store(true)
	go RunDLNAMonitor(t.Context(), MonitorConfig{
		Generation: 5, Clock: clock, Sink: monitorCollector{events},
		GaplessActive: active.Load,
	}, dlna, callbacks)

	// Arm the stop gate so the boundary STOPPED isn't taken for the initial one.
	callbacks <- DLNACallbackEvent{Generation: 5, TransportState: "PLAYING"}
	if event := waitMonitor(t, events); event.State != "PLAYING" {
		t.Fatalf("playing = %#v", event)
	}
	// A track-boundary STOPPED while a gapless next is staged must not complete
	// playback. The follow-up tick still observes NextURI, proving the monitor
	// kept running instead of exiting on the STOPPED.
	callbacks <- DLNACallbackEvent{Generation: 5, TransportState: "STOPPED"}
	clock.tick.ch <- time.Time{}
	if event := waitMonitor(t, events); event.Terminal != "" || !event.NextURIObserved {
		t.Fatalf("boundary stop not deferred: %#v", event)
	}
	// Once gapless disengages without a promotion, the deferred stop surfaces as
	// end-of-media rather than being lost.
	active.Store(false)
	clock.tick.ch <- time.Time{}
	if event := waitMonitor(t, events); event.Terminal != TerminalFinished {
		t.Fatalf("deferred completion = %#v", event)
	}
}

func TestDLNAMonitorProgressArmsCompletionWithoutPlayingCallback(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 2)
	callbacks := make(chan DLNACallbackEvent, 1)
	ctx := t.Context()
	dlna := &seekDLNA{pos: Position{Current: 1, Duration: 10}}
	go RunDLNAMonitor(ctx, MonitorConfig{Generation: 3, Clock: clock, Sink: monitorCollector{events}}, dlna, callbacks)
	clock.tick.ch <- time.Time{}
	_ = waitMonitor(t, events)
	callbacks <- DLNACallbackEvent{Generation: 3, TransportState: "STOPPED"}
	if event := waitMonitor(t, events); event.Terminal != TerminalFinished {
		t.Fatalf("terminal = %#v", event)
	}
}

func TestDLNAMonitorPositionErrorsRemainNonterminal(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 4)
	callbacks := make(chan DLNACallbackEvent, 2)
	ctx := t.Context()
	dlna := &failingPositionDLNA{polled: make(chan struct{})}
	go RunDLNAMonitor(ctx, MonitorConfig{Generation: 8, Clock: clock, Sink: monitorCollector{events}}, dlna, callbacks)

	for range 5 {
		clock.tick.ch <- time.Time{}
		select {
		case <-dlna.polled:
		case <-time.After(time.Second):
			t.Fatal("position polling stopped after errors")
		}
	}

	callbacks <- DLNACallbackEvent{Generation: 8, TransportState: "PLAYING"}
	if event := waitMonitor(t, events); event.State != "PLAYING" {
		t.Fatalf("state = %#v", event)
	}
	callbacks <- DLNACallbackEvent{Generation: 8, TransportState: "STOPPED"}
	if event := waitMonitor(t, events); event.Terminal != TerminalFinished {
		t.Fatalf("terminal = %#v", event)
	}
}

func TestDLNAMonitorSuppressesFirstPrePlayStopped(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 2)
	callbacks := make(chan DLNACallbackEvent)
	ctx := t.Context()
	go RunDLNAMonitor(ctx, MonitorConfig{Generation: 4, Clock: clock, Sink: monitorCollector{events}}, &seekDLNA{}, callbacks)

	send := func() {
		t.Helper()
		select {
		case callbacks <- DLNACallbackEvent{Generation: 4, TransportState: "STOPPED"}:
		case <-time.After(time.Second):
			t.Fatal("callback not consumed")
		}
	}
	send()
	send()
	if event := waitMonitor(t, events); event.Terminal != TerminalFinished {
		t.Fatalf("terminal %q", event.Terminal)
	}
}

func TestDLNAStopGateSurvivesMonitorRestart(t *testing.T) {
	gate := &DLNAStopGate{}
	firstEvents := make(chan MonitorEvent, 2)
	firstCallbacks := make(chan DLNACallbackEvent, 1)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	go RunDLNAMonitor(firstCtx, MonitorConfig{
		Generation:   1,
		Clock:        newManualClock(),
		Sink:         monitorCollector{firstEvents},
		DLNAStopGate: gate,
	}, &seekDLNA{}, firstCallbacks)
	firstCallbacks <- DLNACallbackEvent{Generation: 1, TransportState: "PLAYING"}
	if event := waitMonitor(t, firstEvents); event.State != "PLAYING" {
		t.Fatalf("state = %#v", event)
	}
	cancelFirst()
	_ = waitMonitor(t, firstEvents)

	secondEvents := make(chan MonitorEvent, 1)
	secondCallbacks := make(chan DLNACallbackEvent, 1)
	secondCtx := t.Context()
	go RunDLNAMonitor(secondCtx, MonitorConfig{
		Generation:   2,
		Clock:        newManualClock(),
		Sink:         monitorCollector{secondEvents},
		DLNAStopGate: gate,
	}, &seekDLNA{}, secondCallbacks)
	secondCallbacks <- DLNACallbackEvent{Generation: 2, TransportState: "STOPPED"}
	if event := waitMonitor(t, secondEvents); event.Terminal != TerminalFinished {
		t.Fatalf("terminal = %#v", event)
	}
}

func TestChromecastMonitorStallAndLoss(t *testing.T) {
	t.Run("stall", func(t *testing.T) {
		clock := newManualClock()
		events := make(chan MonitorEvent, 16)
		cast := &seekCast{statuses: []CastStatus{{PlayerState: "PLAYING", Current: 9, Duration: 10}}}
		go RunChromecastMonitor(context.Background(), ChromecastMonitorConfig{MonitorConfig: MonitorConfig{Clock: clock, Sink: monitorCollector{events}}, PlayingStallTicks: 2}, cast)
		for range 4 {
			clock.tick.ch <- time.Time{}
			e := waitMonitor(t, events)
			if e.Terminal == TerminalFinished {
				return
			}
		}
		t.Fatal("stall did not finish")
	})
	t.Run("loss", func(t *testing.T) {
		clock := newManualClock()
		events := make(chan MonitorEvent, 4)
		cast := &seekCast{statusErr: errors.New("lost")}
		go RunChromecastMonitor(context.Background(), ChromecastMonitorConfig{MonitorConfig: MonitorConfig{Clock: clock, Sink: monitorCollector{events}}, LostPolls: 2}, cast)
		clock.tick.ch <- time.Time{}
		clock.tick.ch <- time.Time{}
		e := waitMonitor(t, events)
		if e.Terminal != TerminalError {
			t.Fatalf("terminal %q", e.Terminal)
		}
	})
	t.Run("near-end loss after buffering", func(t *testing.T) {
		clock := newManualClock()
		events := make(chan MonitorEvent, 4)
		lost := errors.New("lost")
		cast := &seekCast{statuses: []CastStatus{
			{PlayerState: "PLAYING", Current: 9, Duration: 10},
			{PlayerState: "BUFFERING"},
		}}
		go RunChromecastMonitor(context.Background(), ChromecastMonitorConfig{
			MonitorConfig: MonitorConfig{Clock: clock, Sink: monitorCollector{events}},
			LostPolls:     2,
		}, cast)

		clock.tick.ch <- time.Time{}
		if event := waitMonitor(t, events); event.Position != 9 || event.Duration != 10 {
			t.Fatalf("playing event %#v", event)
		}
		clock.tick.ch <- time.Time{}
		if event := waitMonitor(t, events); event.State != "BUFFERING" || event.Position != 0 || event.Duration != 0 {
			t.Fatalf("buffering event %#v", event)
		}
		cast.statusErr = lost
		clock.tick.ch <- time.Time{}
		clock.tick.ch <- time.Time{}
		event := waitMonitor(t, events)
		if event.Terminal != TerminalFinished || !errors.Is(event.Err, lost) {
			t.Fatalf("terminal event %#v", event)
		}
	})
	t.Run("transcoded seek uses original duration", func(t *testing.T) {
		clock := newManualClock()
		events := make(chan MonitorEvent, 4)
		lost := errors.New("lost")
		cast := &seekCast{statuses: []CastStatus{{PlayerState: "PLAYING", Current: 84, Duration: 10}}}
		go RunChromecastMonitor(context.Background(), ChromecastMonitorConfig{
			MonitorConfig: MonitorConfig{SeekOffset: 15, ExpectedDuration: 100, Clock: clock, Sink: monitorCollector{events}},
			LostPolls:     2,
		}, cast)

		clock.tick.ch <- time.Time{}
		if event := waitMonitor(t, events); event.Position != 99 || event.Duration != 100 {
			t.Fatalf("seek event %#v", event)
		}
		cast.statusErr = lost
		clock.tick.ch <- time.Time{}
		clock.tick.ch <- time.Time{}
		if event := waitMonitor(t, events); event.Terminal != TerminalFinished || !errors.Is(event.Err, lost) {
			t.Fatalf("terminal event %#v", event)
		}
	})
	t.Run("startup idle is retryable completion", func(t *testing.T) {
		clock := newManualClock()
		events := make(chan MonitorEvent, 2)
		cast := &seekCast{statuses: []CastStatus{{PlayerState: "IDLE"}}}
		go RunChromecastMonitor(context.Background(), ChromecastMonitorConfig{
			MonitorConfig:    MonitorConfig{Clock: clock, Sink: monitorCollector{events}},
			StartupIdleTicks: 2,
		}, cast)

		clock.tick.ch <- time.Time{}
		if event := waitMonitor(t, events); event.State != "IDLE" || event.Terminal != "" {
			t.Fatalf("initial event %#v", event)
		}
		clock.tick.ch <- time.Time{}
		event := waitMonitor(t, events)
		if event.Terminal != TerminalFinished || event.Err == nil {
			t.Fatalf("terminal event %#v", event)
		}
	})
	for _, state := range []string{"PLAYING", "PAUSED"} {
		t.Run("image "+state+" is immediately ready", func(t *testing.T) {
			clock := newManualClock()
			events := make(chan MonitorEvent, 1)
			ctx := t.Context()
			cast := &seekCast{statuses: []CastStatus{{PlayerState: state}}}
			go RunChromecastMonitor(ctx, ChromecastMonitorConfig{
				MonitorConfig: MonitorConfig{Clock: clock, Sink: monitorCollector{events}},
			}, cast)

			clock.tick.ch <- time.Time{}
			if event := waitMonitor(t, events); !event.ImageReady {
				t.Fatalf("%s event %#v", state, event)
			}
		})
	}
	t.Run("image buffering metadata stabilizes", func(t *testing.T) {
		for name, status := range map[string]CastStatus{
			"content type": {PlayerState: "BUFFERING", ContentType: "image/jpeg"},
			"media title":  {PlayerState: "BUFFERING", MediaTitle: "Photo"},
		} {
			t.Run(name, func(t *testing.T) {
				clock := newManualClock()
				events := make(chan MonitorEvent, 2)
				ctx := t.Context()
				cast := &seekCast{statuses: []CastStatus{status}}
				go RunChromecastMonitor(ctx, ChromecastMonitorConfig{
					MonitorConfig: MonitorConfig{Clock: clock, Sink: monitorCollector{events}},
				}, cast)

				clock.tick.ch <- time.Time{}
				if event := waitMonitor(t, events); event.ImageReady {
					t.Fatalf("first metadata event %#v", event)
				}
				clock.tick.ch <- time.Time{}
				if event := waitMonitor(t, events); !event.ImageReady {
					t.Fatalf("stable metadata event %#v", event)
				}
			})
		}
	})
	t.Run("image readiness falls back", func(t *testing.T) {
		clock := newManualClock()
		events := make(chan MonitorEvent, chromecastImageFallbackTicks)
		ctx := t.Context()
		cast := &seekCast{statuses: []CastStatus{{PlayerState: "IDLE"}}}
		go RunChromecastMonitor(ctx, ChromecastMonitorConfig{
			MonitorConfig:    MonitorConfig{Clock: clock, Sink: monitorCollector{events}},
			StartupIdleTicks: chromecastImageFallbackTicks + 1,
		}, cast)

		for tick := 1; tick <= chromecastImageFallbackTicks; tick++ {
			clock.tick.ch <- time.Time{}
			event := waitMonitor(t, events)
			if got, want := event.ImageReady, tick == chromecastImageFallbackTicks; got != want {
				t.Fatalf("tick %d readiness = %t, want %t: %#v", tick, got, want, event)
			}
		}
	})
}

func TestImageTimer(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 1)
	go RunImageTimer(context.Background(), MonitorConfig{Generation: 9, TimerEpoch: 4, Clock: clock, Sink: monitorCollector{events}}, time.Second)
	clock.timer.ch <- time.Time{}
	e := waitMonitor(t, events)
	if e.Terminal != TerminalFinished || e.Generation != 9 || e.TimerEpoch != 4 {
		t.Fatalf("event %#v", e)
	}
}
