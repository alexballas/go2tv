package httphandlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go2tv.app/go2tv/v2/soapcalls"
)

type callbackTestScreen struct {
	msgs      []string
	finished  bool
	mediaType string
}

func (s *callbackTestScreen) EmitMsg(msg string) {
	s.msgs = append(s.msgs, msg)
}

func (s *callbackTestScreen) Fini() {
	s.finished = true
}

func (s *callbackTestScreen) SetMediaType(mediaType string) {
	s.mediaType = mediaType
}

func callbackEventXML(state, actions string) string {
	if actions == "" {
		return `<propertyset><property><LastChange><Event><InstanceID val="0">` +
			`<TransportState val="` + state + `"/>` +
			`</InstanceID></Event></LastChange></property></propertyset>`
	}

	return `<propertyset><property><LastChange><Event><InstanceID val="0">` +
		`<TransportState val="` + state + `"/>` +
		`<CurrentTransportActions val="` + actions + `"/>` +
		`</InstanceID></Event></LastChange></property></propertyset>`
}

func TestCallbackHandlerAcceptsMissingActionsAndRepeatedState(t *testing.T) {
	tv := &soapcalls.TVPayload{
		MediaType:                   "video/mp4",
		MediaRenderersStates:        make(map[string]*soapcalls.States),
		InitialMediaRenderersStates: make(map[string]bool),
	}

	uuid := "cb-1"
	tv.CreateMRstate(uuid)

	screen := &callbackTestScreen{}
	server := NewServer(":0")
	handler := server.callbackHandler(tv, screen)

	for _, body := range []string{
		callbackEventXML("PLAYING", ""),
		callbackEventXML("PLAYING", ""),
	} {
		req := httptest.NewRequest(http.MethodPost, "/cb", strings.NewReader(body))
		req.Header.Set("Sid", "uuid:"+uuid)
		rec := httptest.NewRecorder()
		handler(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status code %d for body %q", rec.Code, body)
		}
	}

	state := tv.MediaRenderersStates[uuid]
	if state.NewState != "PLAYING" {
		t.Fatalf("state got new=%q", state.NewState)
	}

	if len(screen.msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(screen.msgs))
	}

	if screen.msgs[0] != "Playing" || screen.msgs[1] != "Playing" {
		t.Fatalf("unexpected messages: %#v", screen.msgs)
	}
}
