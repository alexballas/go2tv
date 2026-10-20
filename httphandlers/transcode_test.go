package httphandlers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

func TestRetiredMediaCannotPublishTranscodeFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	tt := []struct {
		name   string
		retire func(*HTTPserver, *soapcalls.TVPayload)
	}{
		{name: "remove route", retire: func(s *HTTPserver, _ *soapcalls.TVPayload) { s.RemoveHandler("/movie.mkv") }},
		{name: "replace with native media", retire: func(s *HTTPserver, _ *soapcalls.TVPayload) {
			s.AddHandler("/movie.mkv", nil, nil, []byte("replacement"))
		}},
		{name: "replace with static media", retire: func(s *HTTPserver, _ *soapcalls.TVPayload) {
			s.AddStaticHandler("/movie.mkv", "video/x-matroska", []byte("replacement"))
		}},
		{name: "seek to new transcode", retire: func(s *HTTPserver, p *soapcalls.TVPayload) { s.SetDLNATranscodeSeek(p, 180, "", nil, "", nil) }},
	}
	for _, tc := range tt {
		for _, timing := range []string{"completed failure", "active encoder", "handler selected before encoder starts"} {
			t.Run(tc.name+"/"+timing, func(t *testing.T) {
				dir := t.TempDir()
				ffmpeg := filepath.Join(dir, "ffmpeg")
				script := `#!/bin/sh
if [ "$1" = -hide_banner ]; then exit 1; fi
while [ ! -f "$GO2TV_RETIRED_DIR/release" ]; do sleep 0.01; done
echo 'old encoder failed' >&2
exit 1
`
				if err := os.WriteFile(ffmpeg, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GO2TV_RETIRED_DIR", dir)
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				opened, releaseOpen := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				release := func() {
					t.Helper()
					releaseOnce.Do(func() { close(releaseOpen) })
					if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				media := MediaReaderSeeker(func() (io.ReadSeekCloser, error) {
					close(opened)
					if timing == "handler selected before encoder starts" {
						select {
						case <-releaseOpen:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
					return &testReadSeekCloser{Reader: bytes.NewReader([]byte("input"))}, nil
				})
				server := NewServer("")
				payload := &soapcalls.TVPayload{MediaType: "video/x-matroska", Transcode: true, FFmpegPath: ffmpeg}
				server.AddHandler("/movie.mkv", payload, nil, media)
				done := make(chan struct{})
				go func() {
					defer close(done)
					server.ServeMediaHandler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/movie.mkv", nil).WithContext(ctx))
				}()
				await := func(ch <-chan struct{}) {
					t.Helper()
					select {
					case <-ch:
					case <-ctx.Done():
						t.Fatal("request did not reach expected stage")
					}
				}
				await(opened)
				switch timing {
				case "completed failure":
					release()
					await(done)
					if server.TranscodeStatus("").Err == nil {
						t.Fatal("fixture encoder did not fail")
					}
				case "active encoder":
					for !server.TranscodeStatus("").Active {
						select {
						case <-ctx.Done():
							t.Fatal("encoder did not start")
						case <-time.After(time.Millisecond):
						}
					}
				}
				tc.retire(server, payload)
				release()
				await(done)
				if status := server.TranscodeStatus(""); status.Active || status.Err != nil {
					t.Fatalf("retired request poisoned replacement playback: %+v", status)
				}
			})
		}
	}
}

func TestConcurrentDLNAProbeCannotErasePlaybackFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	tt := []struct {
		name      string
		probeLast bool
	}{
		{name: "probe completes before playback fails"},
		{name: "probe completes after playback fails", probeLast: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ffmpeg := filepath.Join(dir, "ffmpeg")
			script := `#!/bin/sh
if [ "$1" = -hide_banner ]; then exit 1; fi
IFS= read -r mode || exit 1
if [ "$mode" != success ]; then
  printf ready > "$GO2TV_CONCURRENT_DIR/$mode-ready"
  while [ ! -f "$GO2TV_CONCURRENT_DIR/$mode-release" ]; do sleep 0.01; done
fi
if [ "$mode" = fail ]; then echo 'playback encoder failed' >&2; exit 1; fi
printf 'encoded video'
`
			if err := os.WriteFile(ffmpeg, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GO2TV_CONCURRENT_DIR", dir)
			release := func(mode string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, mode+"-release"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var opened atomic.Int32
			media := MediaReaderSeeker(func() (io.ReadSeekCloser, error) {
				mode := "success"
				switch opened.Add(1) {
				case 1:
					mode = "fail"
				case 2:
					if tc.probeLast {
						mode = "probe"
					}
				}
				return &testReadSeekCloser{Reader: bytes.NewReader([]byte(mode + "\n"))}, nil
			})
			server := NewServer("")
			server.AddHandler("/movie.mkv", &soapcalls.TVPayload{MediaType: "video/x-matroska", Transcode: true, FFmpegPath: ffmpeg}, nil, media)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			get := func() <-chan *httptest.ResponseRecorder {
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					response := httptest.NewRecorder()
					server.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/movie.mkv", nil).WithContext(ctx))
					done <- response
				}()
				return done
			}
			awaitReady := func(mode string) {
				t.Helper()
				for {
					if _, err := os.Stat(filepath.Join(dir, mode+"-ready")); err == nil {
						return
					}
					select {
					case <-ctx.Done():
						t.Fatalf("encoder %s did not start", mode)
					case <-time.After(time.Millisecond):
					}
				}
			}
			await := func(done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
				t.Helper()
				select {
				case response := <-done:
					return response
				case <-ctx.Done():
					t.Fatal("transcode did not finish")
					return nil
				}
			}
			playback := get()
			awaitReady("fail")
			probe := get()
			if tc.probeLast {
				awaitReady("probe")
			} else if response := await(probe); response.Code != http.StatusOK {
				t.Fatalf("probe response = %d", response.Code)
			}
			release("fail")
			if response := await(playback); response.Code != http.StatusBadGateway {
				t.Fatalf("playback failure response = %d", response.Code)
			}
			if tc.probeLast {
				status := server.TranscodeStatus("http://receiver/movie.mkv")
				if !status.Active || status.Err == nil || !strings.Contains(status.Err.Error(), "playback encoder failed") {
					t.Fatalf("active probe hid playback failure: %+v", status)
				}
				release("probe")
				if response := await(probe); response.Code != http.StatusOK {
					t.Fatalf("probe response = %d", response.Code)
				}
			}
			status := server.TranscodeStatus("http://receiver/movie.mkv")
			if status.Active || status.Err == nil || !strings.Contains(status.Err.Error(), "playback encoder failed") {
				t.Fatalf("concurrent probe erased playback failure: %+v", status)
			}
			if response := await(get()); response.Code != http.StatusOK {
				t.Fatalf("fresh retry response = %d", response.Code)
			}
			if status := server.TranscodeStatus(""); status.Active || status.Err != nil {
				t.Fatalf("fresh retry retained an obsolete failure: %+v", status)
			}
		})
	}
}

func TestVideoTranscodeReportsFailureAndAllowsRetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	tt := []struct {
		name       string
		chromecast bool
	}{
		{name: "DLNA"},
		{name: "Chromecast", chromecast: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ffmpeg := filepath.Join(dir, "ffmpeg")
			media := filepath.Join(dir, "movie.mkv")
			script := "#!/bin/sh\nif [ \"$1\" = -hide_banner ]; then exit 1; fi\ncase \"$GO2TV_VIDEO_TEST_MODE\" in\nerror) echo 'encoder failed' >&2; exit 1;;\npartial) printf 'encoded video'; echo 'encoder failed' >&2; exit 1;;\nwait) exec sleep 30;;\n*) printf 'encoded video';;\nesac\n"
			if err := os.WriteFile(ffmpeg, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(media, []byte("input"), 0600); err != nil {
				t.Fatal(err)
			}
			server := NewServer("")
			var payload *soapcalls.TVPayload
			var opts *utils.TranscodeOptions
			if tc.chromecast {
				opts = &utils.TranscodeOptions{FFmpegPath: ffmpeg}
			} else {
				payload = &soapcalls.TVPayload{MediaType: "video/x-matroska", Transcode: true, FFmpegPath: ffmpeg}
			}
			server.AddHandler("/movie.mkv", payload, opts, media)
			serve := func(method string) *httptest.ResponseRecorder {
				t.Helper()
				response := httptest.NewRecorder()
				server.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(method, "/movie.mkv", nil))
				return response
			}
			t.Setenv("GO2TV_VIDEO_TEST_MODE", "error")
			serve(http.MethodHead)
			if status := server.TranscodeStatus(""); status.Active || status.Err != nil {
				t.Fatalf("HEAD started transcoding: %+v", status)
			}
			if response := serve(http.MethodGet); response.Code != http.StatusBadGateway {
				t.Fatalf("failed encoder returned HTTP %d", response.Code)
			}
			status := server.TranscodeStatus("http://receiver/movie.mkv")
			if status.Active || status.Err == nil || !strings.Contains(status.Err.Error(), "encoder failed") {
				t.Fatalf("encoder failure was lost: %+v", status)
			}
			t.Setenv("GO2TV_VIDEO_TEST_MODE", "partial")
			if response := serve(http.MethodGet); response.Body.String() != "encoded video" {
				t.Fatalf("error text corrupted the video response: %q", response.Body.String())
			}
			if server.TranscodeStatus("").Err == nil {
				t.Fatal("mid-stream encoder failure was lost")
			}
			t.Setenv("GO2TV_VIDEO_TEST_MODE", "success")
			if response := serve(http.MethodGet); response.Body.String() != "encoded video" {
				t.Fatalf("retry output = %q", response.Body.String())
			}
			if status := server.TranscodeStatus(""); status.Active || status.Err != nil {
				t.Fatalf("successful retry retained the old failure: %+v", status)
			}
			t.Setenv("GO2TV_VIDEO_TEST_MODE", "wait")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				server.ServeMediaHandler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/movie.mkv", nil).WithContext(ctx))
			}()
			deadline := time.Now().Add(5 * time.Second)
			for !server.TranscodeStatus("").Active {
				if time.Now().After(deadline) {
					t.Fatal("slow encoder was not tracked")
				}
				time.Sleep(time.Millisecond)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("receiver cancellation did not stop the encoder")
			}
			if status := server.TranscodeStatus(""); status.Active || status.Err != nil {
				t.Fatalf("receiver cancellation reported as encoder failure: %+v", status)
			}
		})
	}
}
