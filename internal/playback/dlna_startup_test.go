package playback

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestDLNAStartupFailureDoesNotBecomeCompletion(t *testing.T) {
	const current = "http://192.0.2.1:3501/a.mp4"
	transition := dlnaSample{state: "TRANSITIONING", position: Position{Duration: 12, TrackURI: current}}
	stopped := transition
	stopped.state = "STOPPED"
	playing := transition
	playing.state = "PLAYING"
	paused := transition
	paused.state = "PAUSED_PLAYBACK"
	unreachable := transition
	unreachable.stateErr, unreachable.positionErr = errors.New("offline"), errors.New("offline")
	other := stopped
	other.position.TrackURI = "http://192.0.2.1:3500/previous.mp4"
	tt := []struct {
		name    string
		samples []dlnaSample
		times   []int64
		failed  bool
		stopped bool
	}{
		{"rejected after transition", []dlnaSample{transition, stopped, stopped, stopped}, []int64{0, 2, 4, 5}, true, true},
		{"initial STOPPED then starts", []dlnaSample{stopped, stopped, playing}, []int64{0, 2, 4}, false, false},
		{"slow transition still starts", []dlnaSample{transition, transition, playing}, []int64{0, 29, 30}, false, false},
		{"transition timeout", []dlnaSample{transition, transition}, []int64{0, 30}, true, false},
		{"initial STOPPED timeout", []dlnaSample{stopped, stopped}, []int64{0, 30}, true, true},
		{"offline timeout is error", []dlnaSample{unreachable, unreachable}, []int64{0, 30}, true, false},
		{"state failure resets stopped evidence", []dlnaSample{transition, stopped, unreachable, stopped, playing}, []int64{0, 2, 4, 5, 6}, false, false},
		{"stale URI while STOPPED fails early", []dlnaSample{transition, other, other}, []int64{0, 2, 10}, true, false},
		{"pause suspends startup", []dlnaSample{transition, paused, paused, transition, playing}, []int64{0, 1, 50, 51, 52}, false, false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dlna := &scriptedDLNA{samples: tc.samples}
			state, currentURI := "", current
			position, duration := -1, 0
			var progress, boundary, sampled time.Time
			gate, startup := &DLNAStopGate{}, &dlnaStartup{}
			var last MonitorEvent
			for i, seconds := range tc.times {
				var terminal bool
				last, terminal = pollDLNA(t.Context(), MonitorConfig{}, dlna, time.Unix(100+seconds, 0), &state, &currentURI, &position, &duration, &progress, &boundary, &sampled, new(error), gate, startup)
				if last.Terminal == TerminalFinished || terminal && (last.Terminal != TerminalError || i != len(tc.times)-1) {
					t.Fatalf("premature completion at %d: %#v", seconds, last)
				}
			}
			if (last.Terminal == TerminalError) != tc.failed || tc.failed && (last.Err == nil || last.DLNAStopped != tc.stopped) {
				t.Fatalf("startup result = %#v, want failed=%t stopped=%t", last, tc.failed, tc.stopped)
			}
		})
	}
}

type retryDLNA struct {
	scriptedDLNA
	retries []string
}

func (d *retryDLNA) RetryStart(_ context.Context, uri string, reload bool) error {
	d.retries = append(d.retries, fmt.Sprint(uri, " reload=", reload))
	return nil
}

func TestDLNAStartupRetriesDroppedPlay(t *testing.T) {
	const current = "http://192.0.2.1:3501/next.mp4"
	stale := dlnaSample{state: "STOPPED", position: Position{Duration: 20, TrackURI: "http://192.0.2.1:3500/previous.mp4"}}
	loaded := dlnaSample{state: "STOPPED", position: Position{Duration: 3, TrackURI: current}}
	playing := dlnaSample{state: "PLAYING", position: Position{Current: 1, Duration: 3, TrackURI: current}}
	tt := []struct {
		name    string
		samples []dlnaSample
		times   []int64
		retries []string
		failed  bool
	}{
		{"stale track reloads then plays", []dlnaSample{stale, stale, stale, playing}, []int64{0, 2, 4, 5}, []string{current + " reload=true"}, false},
		{"loaded but stopped replays", []dlnaSample{loaded, loaded, loaded, playing}, []int64{0, 2, 4, 5}, []string{current + " reload=false"}, false},
		{"retries exhausted fail before timeout", []dlnaSample{loaded}, []int64{0, 4, 5, 8, 9, 12}, []string{current + " reload=false", current + " reload=false"}, true},
		{"slow transition is not retried", []dlnaSample{{state: "TRANSITIONING", position: Position{TrackURI: current}}}, []int64{0, 10, 20}, nil, false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dlna := &retryDLNA{scriptedDLNA: scriptedDLNA{samples: tc.samples}}
			state, currentURI := "", current
			position, duration := -1, 0
			var progress, boundary, sampled time.Time
			gate, startup := &DLNAStopGate{}, &dlnaStartup{}
			var last MonitorEvent
			var terminal bool
			for _, seconds := range tc.times {
				if terminal {
					t.Fatalf("terminal before %ds: %#v", seconds, last)
				}
				last, terminal = pollDLNA(t.Context(), MonitorConfig{}, dlna, time.Unix(100+seconds, 0), &state, &currentURI, &position, &duration, &progress, &boundary, &sampled, new(error), gate, startup)
			}
			if fmt.Sprint(dlna.retries) != fmt.Sprint(tc.retries) {
				t.Fatalf("retries = %q, want %q", dlna.retries, tc.retries)
			}
			if failed := terminal && last.Terminal == TerminalError; failed != tc.failed {
				t.Fatalf("startup result = %#v, want failed=%t", last, tc.failed)
			}
			if !tc.failed && last.Terminal != "" {
				t.Fatalf("unexpected terminal: %#v", last)
			}
		})
	}
}

func TestDLNAStartupHidesStaleTrackProgress(t *testing.T) {
	const current = "http://192.0.2.1:3501/next.mp4"
	stale := dlnaSample{state: "STOPPED", position: Position{Duration: 20, TrackURI: "http://192.0.2.1:3500/previous.mp4"}}
	loaded := dlnaSample{state: "TRANSITIONING", position: Position{Duration: 3, TrackURI: current}}
	dlna := &scriptedDLNA{samples: []dlnaSample{stale, loaded}}
	state, currentURI := "", current
	position, duration := -1, 0
	var progress, boundary, sampled time.Time
	gate, startup := &DLNAStopGate{}, &dlnaStartup{}
	for i := range dlna.samples {
		event, _ := pollDLNA(t.Context(), MonitorConfig{}, dlna, time.Unix(100+int64(i), 0), &state, &currentURI, &position, &duration, &progress, &boundary, &sampled, new(error), gate, startup)
		if event.Duration != 0 || event.Position != 0 {
			t.Fatalf("pre-start sample %d reached UI: %#v", i, event)
		}
	}
	if duration == 20 {
		t.Fatal("stale track duration fed tail detection")
	}
}

func TestSameTrackURI(t *testing.T) {
	tt := []struct {
		name            string
		observed, wants string
		same            bool
	}{
		{"identical", "http://h:1/a.mp4", "http://h:1/a.mp4", true},
		{"re-escaped", "http://h:1/a%20b.mp4", "http://h:1/a b.mp4", true},
		{"unknown placeholder", "NOT_IMPLEMENTED", "http://h:1/a.mp4", true},
		{"omitted", "", "http://h:1/a.mp4", true},
		{"previous session port", "http://h:3500/a.mp4", "http://h:3501/a.mp4", false},
		{"different file", "http://h:1/b.mp4", "http://h:1/a.mp4", false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameTrackURI(tc.observed, tc.wants); got != tc.same {
				t.Fatalf("sameTrackURI(%q, %q) = %t, want %t", tc.observed, tc.wants, got, tc.same)
			}
		})
	}
}

type deadlineDLNA struct {
	state    func(context.Context) (string, error)
	position func(context.Context) (Position, error)
	media    func(context.Context) (string, string, error)
}

func (d deadlineDLNA) TransportState(ctx context.Context) (string, error)    { return d.state(ctx) }
func (d deadlineDLNA) Position(ctx context.Context) (Position, error)        { return d.position(ctx) }
func (d deadlineDLNA) MediaInfo(ctx context.Context) (string, string, error) { return d.media(ctx) }

func TestDLNAPollOperationsHaveIndependentDeadlines(t *testing.T) {
	for _, operation := range []string{"transport_state", "position"} {
		t.Run(operation+" timeout", func(t *testing.T) {
			dlna := deadlineDLNA{
				state: func(ctx context.Context) (string, error) {
					if operation == "transport_state" {
						<-ctx.Done()
						return "", ctx.Err()
					}
					return "PLAYING", nil
				},
				position: func(ctx context.Context) (Position, error) {
					if operation == "position" {
						<-ctx.Done()
						return Position{}, ctx.Err()
					}
					return Position{Current: 1, Duration: 100, TrackURI: "a"}, ctx.Err()
				},
				media: func(ctx context.Context) (string, string, error) { return "b", "", ctx.Err() },
			}
			clock := newManualClock()
			events := make(chan MonitorEvent, 4)
			go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}, CurrentURI: "a", GaplessActive: func() bool { return true }, QueuedURI: func() string { return "b" }}, dlna, nil)
			clock.tick.ch <- time.Unix(100, 0)
			select {
			case event := <-events:
				if event.Terminal != "" || !event.NextURIObserved || operation == "transport_state" && event.Position != 1 || operation == "position" && !event.GaplessPromoted {
					t.Fatalf("one timeout starved independent poll: %#v", event)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("bounded SOAP timeout did not release monitor")
			}
		})
	}
}

func TestDLNAVerifiedStopBypassesUnresponsivePosition(t *testing.T) {
	for _, gapless := range []bool{false, true} {
		name := "ordinary autoplay"
		if gapless {
			name = "gapless fallback"
		}
		t.Run(name, func(t *testing.T) {
			clock := newManualClock()
			events := make(chan MonitorEvent, 4)
			gate := &DLNAStopGate{}
			gate.Arm()
			dlna := deadlineDLNA{
				state: func(context.Context) (string, error) { return "STOPPED", nil },
				position: func(context.Context) (Position, error) {
					t.Error("position requested after STOPPED was verified")
					return Position{}, errors.New("position unavailable at EOF")
				},
			}
			go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}, DLNAStopGate: gate, GaplessActive: func() bool { return gapless }}, dlna, nil)
			clock.tick.ch <- time.Unix(100, 0)
			if gapless {
				if event := waitMonitor(t, events); event.Terminal != "" {
					t.Fatalf("gapless grace skipped: %#v", event)
				}
				clock.tick.ch <- time.Unix(103, 0)
			}
			if event := waitMonitor(t, events); event.Terminal != TerminalFinished || !event.DLNAStopped {
				t.Fatalf("verified stop did not complete promptly: %#v", event)
			}
		})
	}
}

type busyDLNA struct{ deadlineDLNA }

func (busyDLNA) TryMonitorLock() (func(), bool) { return nil, false }

func TestDLNAStartupTimeoutSurvivesBusyQueue(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 4)
	go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}}, busyDLNA{}, nil)
	clock.tick.ch <- time.Unix(100, 0)
	clock.tick.ch <- time.Unix(130, 0)
	if event := waitMonitor(t, events); event.Terminal != TerminalError || event.Err == nil || event.DLNAStopped {
		t.Fatalf("queue preparation left startup unbounded: %#v", event)
	}
}

func TestDLNAStartupSeekOffsetDoesNotProvePlayback(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 4)
	transition := dlnaSample{state: "TRANSITIONING", position: Position{TrackURI: "a", Duration: 60}}
	stopped := transition
	stopped.state = "STOPPED"
	go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}, CurrentURI: "a", SeekOffset: 24, ExpectedDuration: 60}, &scriptedDLNA{samples: []dlnaSample{transition, stopped, stopped}}, nil)
	for _, seconds := range []int64{0, 2, 5} {
		clock.tick.ch <- time.Unix(100+seconds, 0)
	}
	// Pre-start samples emit nothing, so the first event is the startup verdict.
	if event := waitMonitor(t, events); event.Terminal != TerminalError || event.Err == nil {
		t.Fatalf("transcode offset incorrectly armed playback completion: %#v", event)
	}
}
