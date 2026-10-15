package playback

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type dlnaSample struct {
	position    Position
	state       string
	nextURI     string
	currentURI  string
	stateErr    error
	positionErr error
}

type scriptedDLNA struct {
	samples []dlnaSample
	index   int
	current dlnaSample
}

func (d *scriptedDLNA) Position(context.Context) (Position, error) {
	return d.current.position, d.current.positionErr
}
func (d *scriptedDLNA) TransportState(context.Context) (string, error) {
	d.current = d.samples[min(d.index, len(d.samples)-1)]
	d.index++
	return d.current.state, d.current.stateErr
}
func (d *scriptedDLNA) MediaInfo(context.Context) (string, string, error) {
	return d.current.currentURI, d.current.nextURI, nil
}

func TestDLNAMonitorRecoversStuckEndings(t *testing.T) {
	playing := dlnaSample{position: Position{Current: 99, Duration: 100, TrackURI: "a"}, state: "PLAYING"}
	paused := playing
	paused.state = "PAUSED_PLAYBACK"
	stopped := dlnaSample{state: "STOPPED"}
	startup := dlnaSample{state: "STOPPED", position: Position{Duration: 100}}
	seekBack := playing
	seekBack.position.Current = 20
	unreachable := playing
	unreachable.stateErr = errors.New("status unavailable")
	tt := []struct {
		name     string
		samples  []dlnaSample
		finished bool
		quiet    bool
	}{
		{name: "missing STOPPED callback", samples: []dlnaSample{playing, stopped}, finished: true},
		{name: "stuck PLAYING at final second", samples: []dlnaSample{playing, playing, playing, playing, playing}, finished: true},
		{name: "pause at final second", samples: []dlnaSample{playing, paused, paused, paused, paused}},
		{name: "seek back resets deadline", samples: []dlnaSample{playing, playing, playing, seekBack, seekBack}},
		{name: "network errors never mean finished", samples: []dlnaSample{playing, unreachable, unreachable, unreachable, unreachable}},
		{name: "network recovers without premature EOF", samples: []dlnaSample{playing, unreachable, unreachable, unreachable, playing}},
		{name: "startup STOPPED is not EOF", samples: []dlnaSample{startup, startup, startup}, quiet: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			clock := newManualClock()
			events := make(chan MonitorEvent, 16)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			go RunDLNAMonitor(ctx, MonitorConfig{Clock: clock, CurrentURI: "a", Sink: monitorCollector{events}}, &scriptedDLNA{samples: tc.samples}, nil)
			finished := false
			for i := range tc.samples {
				clock.tick.ch <- time.Unix(100+int64(i), 0)
				var event MonitorEvent
				if tc.quiet {
					// Pre-start samples are silent; only a wrong terminal would surface.
					select {
					case event = <-events:
					case <-time.After(100 * time.Millisecond):
					}
				} else {
					event = waitMonitor(t, events)
				}
				if event.Terminal == TerminalFinished {
					finished = true
					if i != len(tc.samples)-1 {
						t.Fatalf("advanced early on sample %d", i)
					}
				}
			}
			if finished != tc.finished {
				t.Fatalf("finished=%t, want %t", finished, tc.finished)
			}

		})
	}
}

func TestDLNAMonitorGaplessRequiresPlayingQueuedURI(t *testing.T) {
	tt := []struct {
		name     string
		last     dlnaSample
		promoted bool
	}{
		{name: "empty next with old track", last: dlnaSample{position: Position{Current: 9, Duration: 10, TrackURI: "a"}, state: "PLAYING"}},
		{name: "queued track selected but stopped", last: dlnaSample{position: Position{Duration: 10, TrackURI: "b"}, state: "STOPPED"}},
		{name: "queued track playing", last: dlnaSample{position: Position{Current: 1, Duration: 10, TrackURI: "b"}, state: "PLAYING"}, promoted: true},
		{name: "media URI fallback", last: dlnaSample{position: Position{Current: 1, Duration: 10}, currentURI: "b", state: "PLAYING"}, promoted: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			clock := newManualClock()
			events := make(chan MonitorEvent, 4)
			dlna := &scriptedDLNA{samples: []dlnaSample{tc.last}}
			go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}, CurrentURI: "a", GaplessActive: func() bool { return true }, QueuedURI: func() string { return "b" }}, dlna, nil)
			clock.tick.ch <- time.Unix(100, 0)
			event := waitMonitor(t, events)
			if event.GaplessPromoted != tc.promoted || event.Terminal != "" {
				t.Fatalf("handoff = %#v", event)
			}
		})
	}
}

func TestDLNAMonitorGaplessStopHasBoundedFallback(t *testing.T) {
	playing := dlnaSample{position: Position{Current: 9, Duration: 10, TrackURI: "a"}, state: "PLAYING", nextURI: "b"}
	stopped := playing
	stopped.state = "STOPPED"
	stopped.positionErr = errors.New("no current track after STOPPED")
	// Samsung: STOPPED for ~1.2s while it switches to the staged URI.
	switching := stopped
	switching.currentURI = "b"
	next := dlnaSample{position: Position{Current: 1, Duration: 20, TrackURI: "b"}, state: "PLAYING", currentURI: "b"}
	tt := []struct {
		name     string
		samples  []dlnaSample
		times    []int64
		fallback bool
		promoted bool
	}{
		{"rejected handoff falls back after grace", []dlnaSample{playing, stopped, stopped, stopped}, []int64{100, 101, 103, 104}, true, false},
		{"brief boundary STOPPED is tolerated", []dlnaSample{playing, stopped, stopped, next}, []int64{100, 101, 103, 103}, false, true},
		{"renderer switching gets extended wait", []dlnaSample{playing, switching, switching, next}, []int64{100, 101, 108, 109}, false, true},
		{"switching renderer still has a bound", []dlnaSample{playing, switching, switching, switching}, []int64{100, 101, 108, 111}, true, false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			clock := newManualClock()
			events := make(chan MonitorEvent, 8)
			dlna := &scriptedDLNA{samples: tc.samples}
			go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}, CurrentURI: "a", GaplessActive: func() bool { return true }, QueuedURI: func() string { return "b" }}, dlna, nil)
			for i, seconds := range tc.times {
				clock.tick.ch <- time.Unix(seconds, 0)
				event := waitMonitor(t, events)
				last := i == len(tc.times)-1
				if !last && (event.Terminal != "" || event.GaplessPromoted) {
					t.Fatalf("handoff resolved early at %ds: %#v", seconds, event)
				}
				if last && tc.fallback && (event.Terminal != TerminalFinished || event.Recovery != "gapless handoff did not start") {
					t.Fatalf("missing fallback: %#v", event)
				}
				if last && event.GaplessPromoted != tc.promoted {
					t.Fatalf("promotion = %#v, want promoted=%t", event, tc.promoted)
				}
			}
		})
	}
}

func TestDLNAMonitorLateStopCannotFinishNextTrack(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 8)
	callbacks := make(chan DLNACallbackEvent)
	dlna := &scriptedDLNA{samples: []dlnaSample{{position: Position{Current: 1, Duration: 100, TrackURI: "b"}, state: "PLAYING"}}}
	go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}, CurrentURI: "b"}, dlna, callbacks)
	clock.tick.ch <- time.Unix(100, 0)
	_ = waitMonitor(t, events)
	callbacks <- DLNACallbackEvent{TransportState: "STOPPED"}
	clock.tick.ch <- time.Unix(101, 0)
	if event := waitMonitor(t, events); event.Terminal != "" || event.State != "PLAYING" {
		t.Fatalf("late stop ended active track: %#v", event)
	}
}

// tailDLNA plays a 5s track in real time and records every position request.
type tailDLNA struct {
	at        int64
	positions []int64
}

func (d *tailDLNA) TransportState(context.Context) (string, error) {
	if d.at >= 5 {
		return "STOPPED", nil
	}
	return "PLAYING", nil
}

func (d *tailDLNA) Position(context.Context) (Position, error) {
	d.positions = append(d.positions, d.at)
	return Position{Current: int(d.at), Duration: 5, TrackURI: "a"}, nil
}

// Samsung renderers freeze for ~10s when GetPositionInfo lands near the last frame.
func TestDLNAMonitorWithholdsPositionAtTrackEnd(t *testing.T) {
	dlna := &tailDLNA{}
	state, currentURI := "", "a"
	position, duration := -1, 0
	var progress, boundary, sampled time.Time
	gate, startup := &DLNAStopGate{}, &dlnaStartup{}
	var shown []int
	for second := range int64(6) {
		dlna.at = second
		event, terminal := pollDLNA(t.Context(), MonitorConfig{}, dlna, &dlnaDebug{}, time.Unix(100+second, 0), &state, &currentURI, &position, &duration, &progress, &boundary, &sampled, new(error), gate, startup)
		if terminal {
			if second != 5 || event.Terminal != "" || !event.DLNAStopped {
				t.Fatalf("unexpected completion at %ds: %#v", second, event)
			}
			break
		}
		shown = append(shown, event.Position)
	}
	for _, at := range dlna.positions {
		if at > 5-3 {
			t.Fatalf("position requested %ds before end: requests at %v", 5-at, dlna.positions)
		}
	}
	if fmt.Sprint(shown) != "[0 1 2 3 4]" {
		t.Fatalf("UI progress = %v, want continuous estimate", shown)
	}
}

type nowClock struct {
	*manualClock
	now time.Time
}

func (c nowClock) Now() time.Time { return c.now }

// A monitor restarted by a seek near the end must not open with GetPositionInfo.
func TestDLNAMonitorSeededTailSkipsFirstPosition(t *testing.T) {
	clock := nowClock{newManualClock(), time.Unix(100, 0)}
	events := make(chan MonitorEvent, 4)
	gate := &DLNAStopGate{}
	gate.Arm()
	dlna := &tailDLNA{at: 3}
	go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}, CurrentURI: "a", DLNAStopGate: gate, InitialPosition: 3, InitialDuration: 5}, dlna, nil)
	clock.tick.ch <- time.Unix(101, 0)
	if event := waitMonitor(t, events); event.Position != 3 || event.Duration != 5 {
		t.Fatalf("seeded estimate = %#v", event)
	}
	if len(dlna.positions) != 0 {
		t.Fatalf("position requested right after seek near end: %v", dlna.positions)
	}
}

// faultStateDLNA answers GetTransportInfo with a SOAP fault, as some renderers do.
type faultStateDLNA struct{ tailDLNA }

func (*faultStateDLNA) TransportState(context.Context) (string, error) {
	return "", errors.New("SOAP fault s:Client UPnPError: 401 Invalid Action")
}

// A STOPPED callback must end the track when the renderer cannot report state.
func TestDLNAStoppedCallbackTrustedWithoutTransportState(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 4)
	callbacks := make(chan DLNACallbackEvent, 2)
	gate := &DLNAStopGate{}
	gate.Arm()
	go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}, CurrentURI: "a", DLNAStopGate: gate}, &faultStateDLNA{}, callbacks)
	callbacks <- DLNACallbackEvent{TransportState: "STOPPED"}
	for {
		event := waitMonitor(t, events)
		if event.Terminal == "" {
			continue
		}
		if event.Terminal != TerminalFinished {
			t.Fatalf("stop callback ignored: %#v", event)
		}
		return
	}
}

// Renderers may re-escape the staged URI; the handoff must still be recognised.
func TestDLNAGaplessPromotionToleratesReencodedURI(t *testing.T) {
	queued := "http://192.0.2.1:3500/dlna/X/next%20clip.mp4"
	sample := dlnaSample{state: "PLAYING", position: Position{Current: 1, Duration: 20, TrackURI: "http://192.0.2.1:3500/dlna/X/next clip.mp4"}}
	state, currentURI := "", "http://192.0.2.1:3500/current.mp4"
	position, duration := -1, 0
	var progress, boundary, sampled time.Time
	gate := &DLNAStopGate{}
	gate.Arm()
	event, _ := pollDLNA(t.Context(), MonitorConfig{GaplessActive: func() bool { return true }, QueuedURI: func() string { return queued }}, &scriptedDLNA{samples: []dlnaSample{sample}}, &dlnaDebug{}, time.Unix(100, 0), &state, &currentURI, &position, &duration, &progress, &boundary, &sampled, new(error), gate, &dlnaStartup{})
	if !event.GaplessPromoted || event.GaplessURI != queued {
		t.Fatalf("re-encoded handoff not promoted: %#v", event)
	}
}

// steppedClock lets callback and tick polls share one test timeline.
type steppedClock struct {
	*manualClock
	now atomic.Int64
}

func (c *steppedClock) Now() time.Time { return time.Unix(c.now.Load(), 0) }

func (c *steppedClock) step(second int64) {
	c.now.Store(second)
	c.tick.ch <- time.Unix(second, 0)
}

// quietMonitor fails on any terminal event emitted shortly after a poll.
func quietMonitor(t *testing.T, events <-chan MonitorEvent, step string) {
	t.Helper()
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case event := <-events:
			if event.Terminal != "" {
				t.Fatalf("%s: premature terminal %#v", step, event)
			}
		case <-deadline:
			return
		}
	}
}

// With gapless staged, a stateless renderer's STOPPED ends the track as a
// failed handoff, but only once the gapless grace has passed.
func TestDLNAStoppedCallbackTrustedWithGaplessQueued(t *testing.T) {
	clock := &steppedClock{manualClock: newManualClock()}
	clock.now.Store(100)
	events := make(chan MonitorEvent, 16)
	callbacks := make(chan DLNACallbackEvent, 2)
	gate := &DLNAStopGate{}
	gate.Arm()
	go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}, CurrentURI: "a", DLNAStopGate: gate, GaplessActive: func() bool { return true }, QueuedURI: func() string { return "b" }}, &faultStateDLNA{}, callbacks)
	callbacks <- DLNACallbackEvent{TransportState: "STOPPED"}
	quietMonitor(t, events, "first fault")
	clock.step(101)
	quietMonitor(t, events, "fault within grace")
	clock.step(100 + int64(dlnaGaplessGrace/time.Second))
	for {
		event := waitMonitor(t, events)
		if event.Terminal == "" {
			continue
		}
		if event.Terminal != TerminalFinished || event.Recovery != RecoveryGaplessHandoffFailed {
			t.Fatalf("gapless stop not resolved: %#v", event)
		}
		return
	}
}

// A STOPPED callback at a gapless boundary is settled once the queued track
// plays; a later state fault mid-track must not end it.
func TestDLNAStoppedCallbackClearedByPromotion(t *testing.T) {
	fault := dlnaSample{stateErr: errors.New("SOAP fault s:Client UPnPError: 701 Transition not available")}
	playingNext := dlnaSample{state: "PLAYING", position: Position{Current: 1, Duration: 100, TrackURI: "b"}}
	dlna := &scriptedDLNA{samples: []dlnaSample{fault, playingNext, fault}}
	clock := &steppedClock{manualClock: newManualClock()}
	clock.now.Store(100)
	events := make(chan MonitorEvent, 16)
	callbacks := make(chan DLNACallbackEvent, 2)
	gate := &DLNAStopGate{}
	gate.Arm()
	// Owners stop staging once the queued track is promoted.
	var gapless atomic.Bool
	gapless.Store(true)
	go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}, CurrentURI: "a", DLNAStopGate: gate, GaplessActive: gapless.Load, QueuedURI: func() string { return "b" }}, dlna, callbacks)
	callbacks <- DLNACallbackEvent{TransportState: "STOPPED"}
	quietMonitor(t, events, "fault while switching")
	clock.step(101)
	for event := waitMonitor(t, events); !event.GaplessPromoted; event = waitMonitor(t, events) {
		if event.Terminal != "" {
			t.Fatalf("handoff ended track: %#v", event)
		}
	}
	gapless.Store(false)
	clock.step(110)
	quietMonitor(t, events, "fault mid-track")
}

// busyOnceFaultDLNA defers the first poll (queue in flight), then lacks state.
type busyOnceFaultDLNA struct {
	faultStateDLNA
	busy atomic.Bool
}

func (d *busyOnceFaultDLNA) TryMonitorLock() (func(), bool) {
	if d.busy.Swap(false) {
		return nil, false
	}
	return func() {}, true
}

// A callback stop whose verification poll was deferred is still a normal end.
func TestDLNADeferredStopVerificationEndsAsNormalStop(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 8)
	callbacks := make(chan DLNACallbackEvent, 2)
	gate := &DLNAStopGate{}
	gate.Arm()
	dlna := &busyOnceFaultDLNA{}
	dlna.busy.Store(true)
	go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}, CurrentURI: "a", DLNAStopGate: gate}, dlna, callbacks)
	callbacks <- DLNACallbackEvent{TransportState: "STOPPED"}
	clock.tick.ch <- time.Unix(101, 0)
	for {
		event := waitMonitor(t, events)
		if event.Terminal == "" {
			continue
		}
		if event.Terminal != TerminalFinished || event.Recovery != "" {
			t.Fatalf("deferred stop = %#v, want normal finish", event)
		}
		return
	}
}
