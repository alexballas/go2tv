package httphandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/soapcalls"
)

func TestRadioDeliversSmallChunksBeforeUpstreamEnds(t *testing.T) {
	tt := []struct {
		name      string
		mediaType string
	}{
		{name: "AAC", mediaType: "audio/aac"},
		{name: "MP3", mediaType: "audio/mpeg"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			nextChunk := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.mediaType)
				_, _ = io.WriteString(w, "first audio chunk")
				w.(http.Flusher).Flush()
				select {
				case <-nextChunk:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, "next audio chunk")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer upstream.Close()
			server := NewServer("")
			server.AddHandler("/radio", &soapcalls.TVPayload{MediaType: tc.mediaType}, nil, RemoteMediaURL{URL: upstream.URL})
			relay := httptest.NewServer(server.ServeMediaHandler())
			defer relay.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, relay.URL+"/radio", nil)
			if err != nil {
				t.Fatalf("receiver request: %v", err)
			}
			response, err := relay.Client().Do(request)
			if err != nil {
				t.Fatalf("radio headers buffered while upstream remains open: %v", err)
			}
			defer response.Body.Close()
			chunk := make([]byte, len("first audio chunk"))
			if _, err := io.ReadFull(response.Body, chunk); err != nil {
				t.Fatalf("radio chunk buffered while upstream remains open: %v", err)
			}
			if string(chunk) != "first audio chunk" {
				t.Fatalf("radio bytes changed: %q", chunk)
			}
			close(nextChunk)
			chunk = make([]byte, len("next audio chunk"))
			if _, err := io.ReadFull(response.Body, chunk); err != nil {
				t.Fatalf("later radio chunk buffered while upstream remains open: %v", err)
			}
			if string(chunk) != "next audio chunk" {
				t.Fatalf("later radio bytes changed: %q", chunk)
			}
		})
	}
}

func TestRadioStreamLogsTerminationCause(t *testing.T) {
	streamErr := errors.New("stream interrupted")
	tt := []struct {
		name   string
		reader io.Reader
		writer http.ResponseWriter
		cancel bool
		reason string
		bytes  int
	}{
		{name: "upstream EOF", reader: strings.NewReader("audio"), reason: "upstream_eof", bytes: 5},
		{name: "EOF with receiver cancellation", reader: strings.NewReader("audio"), cancel: true, reason: "upstream_eof", bytes: 5},
		{name: "upstream read failure", reader: io.MultiReader(strings.NewReader("audio"), radioErrorReader{streamErr}), reason: "upstream_read_error", bytes: 5},
		{name: "receiver write failure", reader: strings.NewReader("audio"), writer: &radioErrorWriter{ResponseRecorder: httptest.NewRecorder(), writeErr: streamErr}, reason: "receiver_write_error"},
		{name: "receiver flush failure", reader: strings.NewReader("audio"), writer: &radioErrorWriter{ResponseRecorder: httptest.NewRecorder(), flushErr: streamErr}, reason: "receiver_flush_error", bytes: 5},
		{name: "receiver cancellation", reader: radioErrorReader{context.Canceled}, cancel: true, reason: "receiver_canceled"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			var log bytes.Buffer
			writer := tc.writer
			if writer == nil {
				writer = httptest.NewRecorder()
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			serveMediaStream(writer, httptest.NewRequestWithContext(ctx, http.MethodGet, "/radio", nil), &soapcalls.TVPayload{LogOutput: &log}, "audio/aac", tc.reader)
			var entry struct {
				Action string
				Path   string
				Reason string
				Bytes  int
				Error  string
			}
			if err := json.Unmarshal(log.Bytes(), &entry); err != nil {
				t.Fatalf("decode stream diagnostics: %v: %s", err, &log)
			}
			if entry.Action != "StreamEnd" || entry.Path != "/radio" || entry.Reason != tc.reason || entry.Bytes != tc.bytes {
				t.Fatalf("wrong stream termination diagnostics: %+v", entry)
			}
			if tc.reason != "upstream_eof" && entry.Error == "" {
				t.Fatal("stream failure diagnostic omitted error")
			}
		})
	}
}

type radioErrorReader struct{ err error }

func (r radioErrorReader) Read([]byte) (int, error) { return 0, r.err }

type radioErrorWriter struct {
	*httptest.ResponseRecorder
	writeErr error
	flushErr error
}

func (w *radioErrorWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.ResponseRecorder.Write(p)
}

func (w *radioErrorWriter) FlushError() error { return w.flushErr }

func TestRadioRelayReportsICYTitlesWithoutSecondConnection(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "audio/mpeg")
		if r.Header.Get("Icy-MetaData") != "1" {
			_, _ = io.WriteString(w, "audio")
			return
		}
		w.Header().Set("icy-metaint", "3")
		_, _ = io.WriteString(w, "aud\x02StreamTitle='Song';"+strings.Repeat("\x00", 13)+"io")
	}))
	defer upstream.Close()
	titles := make(chan string, 1)
	server := NewServer("")
	server.AddHandler("/radio", &soapcalls.TVPayload{MediaType: "audio/mpeg"}, nil, RemoteMediaURL{URL: upstream.URL, OnTitle: func(title string) { titles <- title }})
	relay := httptest.NewServer(server.ServeMediaHandler())
	defer relay.Close()
	response, err := relay.Client().Get(relay.URL + "/radio")
	if err != nil {
		t.Fatalf("receiver request: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "audio" {
		t.Fatalf("renderer received %q, %v", body, err)
	}
	select {
	case title := <-titles:
		if title != "Song" || requests.Load() != 1 {
			t.Fatalf("title %q after %d upstream requests", title, requests.Load())
		}
	default:
		t.Fatal("relayed ICY title not reported")
	}
}
