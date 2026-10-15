package soapcalls

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDLNAMonitorRejectsSOAPFailures(t *testing.T) {
	fault := `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>401</errorCode><errorDescription>Invalid Action</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`
	tt := []struct {
		name   string
		status int
		body   string
	}{
		{"HTTP error", http.StatusInternalServerError, fault},
		{"fault with HTTP success", http.StatusOK, fault},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			payload := &TVPayload{ControlURL: server.URL, MediaURL: "http://127.0.0.1/next.mp4", MediaType: "video/mp4", LogOutput: io.Discard}
			if err := payload.SetNextAVTransportURI(false); err == nil {
				t.Fatal("rejected queue reported success")
			}
			if uri, err := payload.Gapless(); err == nil {
				t.Fatalf("failed media poll reported URI %q", uri)
			}
			if _, err := payload.GetTransportInfo(); err == nil {
				t.Fatal("failed state poll reported success")
			}
			if _, err := payload.GetPositionInfo(); err == nil {
				t.Fatal("failed position poll reported success")
			}
		})
	}
}

func TestDLNAMediaPollRequiresResponseAndKeepsTrackIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.Contains(r.Header.Get("SOAPAction"), "GetPositionInfo") {
			_, _ = io.WriteString(w, `<Envelope><Body><GetPositionInfoResponse><TrackDuration>00:01:00</TrackDuration><RelTime>00:00:05</RelTime><TrackURI>http://renderer/next.mp4</TrackURI></GetPositionInfoResponse></Body></Envelope>`)
			return
		}
		_, _ = io.WriteString(w, `<Envelope><Body/></Envelope>`)
	}))
	defer server.Close()
	payload := &TVPayload{ControlURL: server.URL, LogOutput: io.Discard}
	if _, err := payload.GetMediaInfoContext(t.Context()); err == nil {
		t.Fatal("missing media response interpreted as consumed next URI")
	}
	position, err := payload.GetPositionInfoContext(t.Context())
	if err != nil || position.TrackURI != "http://renderer/next.mp4" || position.RelTime != "00:00:05" {
		t.Fatalf("track identity = %#v, %v", position, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := payload.GetPositionInfoContext(ctx); err == nil {
		t.Fatal("canceled position poll succeeded")
	}
	if payload.Context().Err() != nil {
		t.Fatal("monitor deadline canceled playback lifetime")
	}
}

func TestDLNACleanupSubscriptionsDoesNotSendStop(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "successful unsubscribe"
		if canceled {
			name = "failed unsubscribe still releases local state"
		}
		t.Run(name, func(t *testing.T) {
			requests := make(chan string, 4)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.Method + ":" + r.Header.Get("SID")
			}))
			defer server.Close()
			timer := time.NewTimer(time.Hour)
			defer timer.Stop()
			p := &TVPayload{EventURL: server.URL, ControlURL: server.URL, MediaRenderersStates: map[string]*States{"old": {}}, CurrentTimers: map[string]*time.Timer{"old": timer}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if canceled {
				cancel()
			}
			err := p.CleanupSubscriptionsContext(ctx)
			if (err != nil) != canceled || len(p.SubscriptionIDs()) != 0 || len(p.CurrentTimers) != 0 {
				t.Fatalf("cleanup did not release callbacks/timers: error=%v subscriptions=%v timers=%d", err, p.SubscriptionIDs(), len(p.CurrentTimers))
			}
			if !canceled {
				if request := <-requests; request != "UNSUBSCRIBE:uuid:old" {
					t.Fatalf("cleanup sent %q", request)
				}
			}
			select {
			case request := <-requests:
				t.Fatalf("unexpected remote command %s", request)
			default:
			}
		})
	}
}
