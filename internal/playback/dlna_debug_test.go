package playback

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Temporary diagnostics tests: confirm a device trace explains the recovery
// decision and keeps a prolonged outage from flooding the exported log ring.
type dlnaTraceRecord struct {
	event  string
	fields map[string]any
}

type dlnaTraceCollector struct {
	mu      sync.Mutex
	records []dlnaTraceRecord
}

func (c *dlnaTraceCollector) write(event string, fields ...any) {
	values := make(map[string]any)
	for i := 0; i+1 < len(fields); i += 2 {
		values[fields[i].(string)] = fields[i+1]
	}
	c.mu.Lock()
	c.records = append(c.records, dlnaTraceRecord{event, values})
	c.mu.Unlock()
}

func (c *dlnaTraceCollector) matching(event string) []dlnaTraceRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	var records []dlnaTraceRecord
	for _, record := range c.records {
		if record.event == event {
			records = append(records, record)
		}
	}
	return records
}

func TestDLNADiagnosticsExplainStallRecovery(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 8)
	trace := &dlnaTraceCollector{}
	dlna := &scriptedDLNA{samples: []dlnaSample{{position: Position{Current: 99, Duration: 100, TrackURI: "video-a"}, state: "PLAYING"}}}
	go RunDLNAMonitor(t.Context(), MonitorConfig{Generation: 7, Clock: clock, Sink: monitorCollector{events}, CurrentURI: "video-a", DLNADebug: trace.write}, dlna, nil)
	// Position is withheld through the tail guard, then the stalled sample is final.
	for i := range 5 {
		clock.tick.ch <- time.Unix(100+int64(i), 0)
		event := waitMonitor(t, events)
		if i == 4 && event.Terminal != TerminalFinished {
			t.Fatalf("stall did not complete: %#v", event)
		}
	}
	recoveries := trace.matching("stall_recovery")
	if len(recoveries) != 1 {
		t.Fatalf("recovery trace = %#v", recoveries)
	}
	fields := recoveries[0].fields
	if fields["position"] != 99 || fields["duration"] != 100 || fields["stationary_ms"] != int64(4000) {
		t.Fatalf("missing evidence for EOF inference: %#v", fields)
	}
	completions := trace.matching("completion")
	if len(completions) != 1 || completions[0].fields["reason"] != "position stalled near end" {
		t.Fatalf("completion cause missing: %#v", completions)
	}
}

func TestDLNADiagnosticsSummarizeOutageAndRecovery(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 32)
	trace := &dlnaTraceCollector{}
	playing := dlnaSample{position: Position{Current: 99, Duration: 100}, state: "PLAYING"}
	failed := playing
	failed.stateErr = errors.New("renderer unreachable")
	samples := []dlnaSample{playing}
	for range 20 {
		samples = append(samples, failed)
	}
	samples = append(samples, playing)
	go RunDLNAMonitor(t.Context(), MonitorConfig{Clock: clock, Sink: monitorCollector{events}, DLNADebug: trace.write}, &scriptedDLNA{samples: samples}, nil)
	for i := range samples {
		clock.tick.ch <- time.Unix(100+int64(i), 0)
		if event := waitMonitor(t, events); event.Terminal != "" {
			t.Fatalf("outage was treated as EOF: %#v", event)
		}
	}
	failures, recovered := trace.matching("poll_failed"), trace.matching("poll_recovered")
	if len(failures) == 0 || len(failures) > 3 {
		t.Fatalf("unbounded or missing failure records: %d", len(failures))
	}
	if len(recovered) != 1 || recovered[0].fields["previous_failures"] != 20 || recovered[0].fields["operation"] != "transport_state" {
		t.Fatalf("outage recovery evidence = %#v", recovered)
	}
	if records := trace.matching("end_sample"); len(records) > 8 {
		t.Fatalf("stationary samples flooded diagnostics: %d", len(records))
	}
}

func TestDLNADiagnosticsExplainRejectedLateStop(t *testing.T) {
	clock := newManualClock()
	events := make(chan MonitorEvent, 8)
	callbacks := make(chan DLNACallbackEvent)
	trace := &dlnaTraceCollector{}
	dlna := &scriptedDLNA{samples: []dlnaSample{{position: Position{Current: 3, Duration: 100, TrackURI: "video-b"}, state: "PLAYING"}}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go RunDLNAMonitor(ctx, MonitorConfig{Clock: clock, Sink: monitorCollector{events}, CurrentURI: "video-b", DLNADebug: trace.write}, dlna, callbacks)
	clock.tick.ch <- time.Unix(100, 0)
	_ = waitMonitor(t, events)
	callbacks <- DLNACallbackEvent{TransportState: "STOPPED"}
	if event := waitMonitor(t, events); event.Terminal != "" {
		t.Fatalf("late callback stopped next video: %#v", event)
	}
	verified := trace.matching("stop_verification_result")
	if len(verified) != 1 || verified[0].fields["state"] != "PLAYING" || verified[0].fields["state_confirmed"] != true {
		t.Fatalf("late stop rejection unexplained: %#v", verified)
	}
}
