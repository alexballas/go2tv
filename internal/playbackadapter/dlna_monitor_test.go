package playbackadapter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/soapcalls"
)

func TestDLNAPollPreservesSubscriptionLifetimeAndUnknownDuration(t *testing.T) {
	var subscriptions atomic.Int32
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch {
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `<root><device><serviceList><service><serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType><serviceId>urn:upnp-org:serviceId:AVTransport</serviceId><controlURL>/transport</controlURL><eventSubURL>/events</eventSubURL></service></serviceList></device></root>`)
		case r.Method == "SUBSCRIBE":
			subscriptions.Add(1)
			w.Header().Set("SID", "uuid:renewal")
			w.Header().Set("TIMEOUT", "Second-300")
		case strings.Contains(r.Header.Get("SOAPAction"), "GetPositionInfo"):
			_, _ = io.WriteString(w, `<Envelope><Body><GetPositionInfoResponse><TrackDuration>NOT_IMPLEMENTED</TrackDuration><RelTime>00:00:09</RelTime><TrackURI>http://media/track.mp4</TrackURI></GetPositionInfoResponse></Body></Envelope>`)
		}
	}))
	defer renderer.Close()
	dlna, err := NewDLNA(t.Context(), DLNAConfig{Endpoint: renderer.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dlna.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	pollCtx, cancel := context.WithCancel(t.Context())
	position, err := dlna.Position(pollCtx)
	cancel()
	if err != nil || position.Current != 9 || position.Duration != 0 || position.TrackURI != "http://media/track.mp4" {
		t.Fatalf("position = %#v, %v", position, err)
	}
	dlna.payload.CallbackURL = "http://127.0.0.1/callback"
	if err := dlna.payload.SubscribeSoapCall(""); err != nil {
		t.Fatalf("subscription inherited expired poll context: %v", err)
	}
	if err := dlna.payload.SubscribeSoapCall("renewal"); err != nil {
		t.Fatalf("renewal failed after poll: %v", err)
	}
	if subscriptions.Load() != 2 {
		t.Fatalf("subscriptions = %d", subscriptions.Load())
	}
}

// A startup reload must restore the item being started and re-stage the next one.
func TestDLNARetryStartReloadsCurrentAndRestagesNext(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		action := r.Header.Get("SOAPAction")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(action, "#SetAVTransportURI"):
			calls = append(calls, "set:"+between(string(body), "<CurrentURI>", "</CurrentURI>"))
		case strings.Contains(action, "#SetNextAVTransportURI"):
			calls = append(calls, "next:"+between(string(body), "<NextURI>", "</NextURI>"))
		case strings.Contains(action, "#Play"):
			calls = append(calls, "play")
		}
	}))
	t.Cleanup(renderer.Close)
	payload := &soapcalls.TVPayload{ControlURL: renderer.URL, CurrentTimers: map[string]*time.Timer{}, MediaRenderersStates: map[string]*soapcalls.States{}, InitialMediaRenderersStates: map[string]bool{}}
	payload.SetContext(t.Context())
	d := &DLNA{payload: payload}
	ctx := t.Context()
	if err := d.Load(ctx, playback.LoadRequest{MediaURL: "http://h/a.mp4", MediaType: "video/mp4"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetNext(ctx, playback.LoadRequest{MediaURL: "http://h/b.mp4", MediaType: "video/mp4"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	calls = nil
	mu.Unlock()
	if err := d.RetryStart(ctx, "http://h/a.mp4", true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(calls, ","); got != "set:http://h/a.mp4,next:http://h/b.mp4,play" {
		t.Fatalf("retry calls = %s", got)
	}
}

func between(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	s = s[i+len(open):]
	if j := strings.Index(s, close); j >= 0 {
		return s[:j]
	}
	return s
}
