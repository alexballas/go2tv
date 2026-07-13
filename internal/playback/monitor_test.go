package playback

import (
	"context"
	"errors"
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

func (c *manualClock) Now() time.Time { return time.Time{} }
func (c *manualClock) NewTicker(time.Duration) Ticker {
	return c.tick
}
func (c *manualClock) NewTimer(time.Duration) Timer {
	return c.timer
}

type monitorCollector struct{ ch chan MonitorEvent }

func (c monitorCollector) HandleMonitorEvent(_ context.Context, e MonitorEvent) { c.ch <- e }

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
	callbacks <- DLNACallbackEvent{Generation: 7, TransportState: "STOPPED"}
	e = waitMonitor(t, events)
	if e.Terminal != TerminalFinished {
		t.Fatalf("terminal %q", e.Terminal)
	}
	cancel()

	clock = newManualClock()
	events = make(chan MonitorEvent, 2)
	ctx, cancel = context.WithCancel(WithTerminalReason(context.Background(), TerminalReplacement))
	go RunDLNAMonitor(ctx, MonitorConfig{Clock: clock, Sink: monitorCollector{events}}, d, nil)
	cancel()
	e = waitMonitor(t, events)
	if e.Terminal != TerminalReplacement {
		t.Fatalf("cancel %q", e.Terminal)
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
}

func TestImageTimer(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 1)
	go RunImageTimer(context.Background(), MonitorConfig{Generation: 9, Clock: clock, Sink: monitorCollector{events}}, time.Second)
	clock.timer.ch <- time.Time{}
	e := waitMonitor(t, events)
	if e.Terminal != TerminalFinished || e.Generation != 9 {
		t.Fatalf("event %#v", e)
	}
}
