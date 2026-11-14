package httphandlers

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

func TestStopServerReapsActiveEncoders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	tt := []struct {
		name, mediaType string
		chromecast      bool
	}{
		{name: "DLNA video", mediaType: "video/mp4"},
		{name: "DLNA audio", mediaType: "audio/mpeg"},
		{name: "Chromecast video", mediaType: "video/mp4", chromecast: true},
		{name: "Chromecast audio", mediaType: "audio/mpeg", chromecast: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			ffmpeg, path, entered := blockedShutdownEncoder(t)
			server := NewServer("")
			t.Cleanup(server.StopServer)
			if tc.chromecast {
				server.AddHandler("/media", nil, &utils.TranscodeOptions{FFmpegPath: ffmpeg, AudioOnly: tc.mediaType == "audio/mpeg"}, path)
			} else {
				server.AddHandler("/media", &soapcalls.TVPayload{MediaType: tc.mediaType, Transcode: true, FFmpegPath: ffmpeg}, nil, path)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				server.ServeMediaHandler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/media", nil))
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(entered); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("encoder did not start")
				}
				time.Sleep(time.Millisecond)
			}
			stopped := make(chan struct{})
			go func() { server.StopServer(); close(stopped) }()
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("StopServer did not reap the encoder")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("request survived StopServer")
			}
			response := httptest.NewRecorder()
			server.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/media", nil))
			if response.Code != http.StatusNotFound {
				t.Fatalf("stopped server accepted a new encoder: HTTP %d", response.Code)
			}
		})
	}
}

func blockedShutdownEncoder(t *testing.T) (ffmpeg, path, entered string) {
	t.Helper()
	return fakeEncoder(t, "exec sleep 300")
}

// fakeEncoder answers FFmpeg capability probes, then runs body as the encoder.
func fakeEncoder(t *testing.T, body string) (ffmpeg, path, entered string) {
	t.Helper()
	dir := t.TempDir()
	ffmpeg = filepath.Join(dir, "ffmpeg")
	script := `#!/bin/sh
if [ "$1" = -hide_banner ]; then
  if [ "$2" = -encoders ]; then echo ' V..... libx264 fake'; fi
  if [ "$2" = -encoders ] || [ "$2" = -hwaccels ] || [ "$2" = -filters ] || [ "$2" = -protocols ]; then exit 0; fi
fi
: > "${0%/*}/entered"
` + body + "\n"
	if err := os.WriteFile(ffmpeg, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ffprobe"), []byte("#!/bin/sh\nprintf '{\"streams\":[]}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "movie.mp4")
	if err := os.WriteFile(path, []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}

	return ffmpeg, path, filepath.Join(dir, "entered")
}

func TestChromecastReplacementReapsPreviousEncoder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	ffmpeg, path, entered := blockedShutdownEncoder(t)
	server := NewServer("")
	t.Cleanup(server.StopServer)
	server.AddHandler("/media", nil, &utils.TranscodeOptions{FFmpegPath: ffmpeg}, path)
	start := func() <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			server.ServeMediaHandler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/media", nil))
		}()
		return done
	}
	waitEntered := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(entered); err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("encoder did not start")
			}
			time.Sleep(time.Millisecond)
		}
	}
	first := start()
	waitEntered()
	if err := os.Remove(entered); err != nil {
		t.Fatal(err)
	}
	second := start()
	waitEntered()
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("replacement left the preceding encoder running")
	}
	select {
	case <-second:
		t.Fatal("preceding request killed its replacement")
	default:
	}
	server.StopServer()
	<-second
}

func TestChromecastReplacementSurvivesStalledReceiver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	ffmpeg, path, _ := fakeEncoder(t, "exec cat /dev/zero")
	server := NewServer("")
	t.Cleanup(server.StopServer)
	server.AddHandler("/media", nil, &utils.TranscodeOptions{FFmpegPath: ffmpeg}, path)
	listener := httptest.NewServer(server.ServeMediaHandler())
	t.Cleanup(listener.Close)

	// A receiver that dropped off the network: socket open, never reading.
	stalled, err := net.Dial("tcp", listener.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stalled.Close() })
	if _, err := io.WriteString(stalled, "GET /media HTTP/1.1\r\nHost: cast\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(stalled).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // let socket buffers fill

	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(listener.URL + "/media")
	if err != nil {
		t.Fatalf("reconnecting receiver blocked behind stalled one: %v", err)
	}
	defer response.Body.Close()
	if _, err := io.ReadFull(response.Body, make([]byte, 1024)); err != nil {
		t.Fatalf("replacement stream: HTTP %d: %v", response.StatusCode, err)
	}
}
