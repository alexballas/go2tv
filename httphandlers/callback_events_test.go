package httphandlers

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type callbackCollector struct {
	mu     sync.Mutex
	events []CallbackEvent
	ch     chan CallbackEvent
}

func (c *callbackCollector) HandleCallbackEvent(_ context.Context, event CallbackEvent) {
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
	if c.ch != nil {
		c.ch <- event
	}
}

type gapCollector struct{ ch chan [2]uint32 }

func (g gapCollector) CallbackGap(_ context.Context, _ string, previous, next uint32) {
	g.ch <- [2]uint32{previous, next}
}

type blockingCallbackSink struct {
	entered chan struct{}
	release chan struct{}
}

func (s blockingCallbackSink) HandleCallbackEvent(context.Context, CallbackEvent) {
	s.entered <- struct{}{}
	<-s.release
}

func newNotifyRequest(seq, state string) *http.Request {
	body := callbackEventXML(state, "")
	req := httptest.NewRequest("NOTIFY", "/callback", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("SID", "uuid:session")
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("NTS", "upnp:propchange")
	req.Header.Set("SEQ", seq)
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	return req
}

func TestCallbackSessionValidationAndOrdering(t *testing.T) {
	collector := &callbackCollector{ch: make(chan CallbackEvent, 8)}
	gaps := gapCollector{ch: make(chan [2]uint32, 1)}
	session, err := NewCallbackSession(CallbackSessionConfig{
		Generation: 4, SID: "session", SourceIP: net.ParseIP("192.0.2.10"),
		MediaType: "video/mp4", Sink: collector, GapHandler: gaps,
	})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer session.Close()

	for _, seq := range []string{"4294967295", "0", "0", "2"} {
		rec := httptest.NewRecorder()
		session.Handler()(rec, newNotifyRequest(seq, "PLAYING"))
		if rec.Code != http.StatusOK {
			t.Fatalf("seq %s status %d", seq, rec.Code)
		}
	}
	for range 2 {
		select {
		case <-collector.ch:
		case <-time.After(time.Second):
			t.Fatal("event timeout")
		}
	}
	select {
	case gap := <-gaps.ch:
		if gap != [2]uint32{0, 2} {
			t.Fatalf("gap = %v", gap)
		}
	case <-time.After(time.Second):
		t.Fatal("gap timeout")
	}
	collector.mu.Lock()
	count := len(collector.events)
	collector.mu.Unlock()
	if count != 2 {
		t.Fatalf("events = %d, want 2", count)
	}

	bad := newNotifyRequest("3", "PLAYING")
	bad.Header.Del("NTS")
	rec := httptest.NewRecorder()
	session.Handler()(rec, bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing NTS status %d", rec.Code)
	}

	tooLarge := newNotifyRequest("3", "PLAYING")
	tooLarge.Body = ioNopCloser{strings.NewReader(strings.Repeat("x", MaxCallbackBody+1))}
	rec = httptest.NewRecorder()
	session.Handler()(rec, tooLarge)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large status %d", rec.Code)
	}
}

type ioNopCloser struct{ *strings.Reader }

func (ioNopCloser) Close() error { return nil }

func TestCallbackSessionQueueFull(t *testing.T) {
	sink := blockingCallbackSink{entered: make(chan struct{}, 1), release: make(chan struct{})}
	session, err := NewCallbackSession(CallbackSessionConfig{Generation: 1, SID: "session", QueueSize: 1, Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	defer close(sink.release)
	request := func(seq string) int {
		rec := httptest.NewRecorder()
		session.Handler()(rec, newNotifyRequest(seq, "PLAYING"))
		return rec.Code
	}
	if got := request("1"); got != http.StatusOK {
		t.Fatalf("first status %d", got)
	}
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not block")
	}
	if got := request("2"); got != http.StatusOK {
		t.Fatalf("second status %d", got)
	}
	if got := request("3"); got != http.StatusServiceUnavailable {
		t.Fatalf("full status %d", got)
	}
}

func TestCallbackSessionStoppedWaitsForFullQueue(t *testing.T) {
	sink := blockingCallbackSink{entered: make(chan struct{}, 1), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(sink.release) }) }
	defer release()
	session, err := NewCallbackSession(CallbackSessionConfig{Generation: 1, SID: "session", QueueSize: 1, Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	request := func(seq, state string) int {
		rec := httptest.NewRecorder()
		session.Handler()(rec, newNotifyRequest(seq, state))
		return rec.Code
	}
	if got := request("1", "PLAYING"); got != http.StatusOK {
		t.Fatalf("first status %d", got)
	}
	select {
	case <-sink.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not block")
	}
	if got := request("2", "PLAYING"); got != http.StatusOK {
		t.Fatalf("second status %d", got)
	}

	result := make(chan int, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		result <- request("3", "STOPPED")
	}()
	<-started
	select {
	case status := <-result:
		t.Fatalf("STOPPED returned from full queue: %d", status)
	case <-time.After(10 * time.Millisecond):
	}
	release()
	select {
	case status := <-result:
		if status != http.StatusOK {
			t.Fatalf("STOPPED status %d", status)
		}
	case <-time.After(time.Second):
		t.Fatal("STOPPED remained blocked")
	}
}
