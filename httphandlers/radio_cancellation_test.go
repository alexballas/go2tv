package httphandlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

func TestRadioReceiverCancellationClosesStalledUpstream(t *testing.T) {
	tt := []struct {
		name      string
		acquiring bool
		owned     bool
	}{
		{name: "direct URL acquisition", acquiring: true},
		{name: "direct URL body read"},
		{name: "owned reader body read", owned: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			started, upstreamClosed := make(chan struct{}), make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !tc.acquiring {
					w.Header().Set("Content-Type", "audio/mpeg")
					_, _ = io.WriteString(w, "audio bytes")
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
				close(upstreamClosed)
			}))
			defer upstream.Close()
			defer upstream.CloseClientConnections()
			server := NewServer("")
			var media any = RemoteMediaURL(upstream.URL)
			if tc.owned {
				rc, err := utils.StreamURL(t.Context(), upstream.URL)
				if err != nil {
					t.Fatalf("open upstream: %v", err)
				}
				defer rc.Close()
				media = rc
			}
			server.AddHandler("/radio", &soapcalls.TVPayload{MediaType: "audio/mpeg"}, nil, media)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/radio", nil)
			response := &radioSignalWriter{ResponseRecorder: httptest.NewRecorder(), written: make(chan struct{})}
			done := make(chan struct{})
			go func() {
				server.ServeMediaHandler()(response, request)
				close(done)
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("upstream request did not start")
			}
			if !tc.acquiring {
				select {
				case <-response.written:
				case <-time.After(2 * time.Second):
					t.Fatal("audio did not reach receiver")
				}
			}
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("receiver cancellation left relay handler blocked")
			}
			select {
			case <-upstreamClosed:
			case <-time.After(2 * time.Second):
				t.Fatal("receiver cancellation left upstream connection open")
			}
		})
	}
}

func TestDirectRadioReceiverProbesDoNotOpenUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("HEAD probe opened upstream")
	}))
	defer upstream.Close()
	server := NewServer("")
	server.AddHandler("/radio", &soapcalls.TVPayload{MediaType: "audio/mpeg"}, nil, RemoteMediaURL(upstream.URL))
	response := httptest.NewRecorder()
	server.ServeMediaHandler()(response, httptest.NewRequest(http.MethodHead, "/radio", nil))
	if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("invalid radio probe response: HTTP %d, %v", response.Code, response.Header())
	}
}

func TestCanceledLiveStreamKeepsLeaseUntilReadExits(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	reader := &blockedLiveReader{started: started, release: release}
	var unblockOnce sync.Once
	unblock := func() { unblockOnce.Do(func() { close(release) }) }
	leaseClosed := make(chan struct{})
	var leaseCloseOnce sync.Once
	var mu sync.Mutex
	held := false
	stream := LiveStream(func() (io.ReadCloser, error) {
		mu.Lock()
		defer mu.Unlock()
		if held {
			return nil, errors.New("busy")
		}
		held = true
		return readCloserFunc{
			Reader: reader,
			// Screencast leases release ownership; they do not stop Read.
			closer: func() error {
				mu.Lock()
				held = false
				mu.Unlock()
				leaseCloseOnce.Do(func() { close(leaseClosed) })
				return nil
			},
		}, nil
	})
	server := NewServer("")
	server.AddHandler("/screencast", &soapcalls.TVPayload{MediaType: "video/mpeg"}, nil, stream)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		server.ServeMediaHandler()(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/screencast", nil))
		close(done)
	}()
	defer func() {
		unblock()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("screencast handler did not exit after producer stopped")
		}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("screencast read did not start")
	}
	cancel()
	select {
	case <-leaseClosed:
		t.Fatal("cancellation released a lease whose reader is still blocked")
	case <-time.After(100 * time.Millisecond):
	}
	busy := httptest.NewRecorder()
	server.ServeMediaHandler()(busy, httptest.NewRequest(http.MethodGet, "/screencast", nil))
	if busy.Code != http.StatusServiceUnavailable {
		t.Fatalf("reconnect during blocked read: HTTP %d, want 503", busy.Code)
	}
	unblock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("screencast handler did not release its reader")
	}
	again := httptest.NewRecorder()
	server.ServeMediaHandler()(again, httptest.NewRequest(http.MethodGet, "/screencast", nil))
	if again.Code != http.StatusOK {
		t.Fatalf("reconnect after read exited: HTTP %d, want 200", again.Code)
	}
}

type blockedLiveReader struct {
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (r *blockedLiveReader) Read([]byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return 0, io.EOF
}

type radioSignalWriter struct {
	*httptest.ResponseRecorder
	written chan struct{}
	once    sync.Once
}

func (w *radioSignalWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(data)
	if n > 0 {
		w.once.Do(func() { close(w.written) })
	}
	return n, err
}
