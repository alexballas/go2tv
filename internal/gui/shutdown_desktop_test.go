//go:build !(android || ios)

package gui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/widget"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/rtmp"
)

func shutdownTestScreen(t *testing.T) *FyneScreen {
	t.Helper()
	s, _ := newMediaCardTestScreen(t)
	s.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
	s.CurrentPos, s.EndPos = binding.NewString(), binding.NewString()
	return s
}

func awaitGUIShutdown(t *testing.T, s *FyneScreen) {
	t.Helper()
	select {
	case <-s.beginGUIShutdown():
	case <-time.After(5 * time.Second):
		t.Fatal("GUI shutdown did not reap owned work")
	}
}

func TestGUIShutdownReapsMetadataProbes(t *testing.T) {
	tt := []struct {
		name  string
		start func(*FyneScreen, string)
	}{
		{name: "audio", start: func(s *FyneScreen, _ string) { s.refreshAudioPicker() }},
		{name: "subtitles", start: updateInternalSubsDropdownAsync},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s := shutdownTestScreen(t)
			ffmpeg, entered, _ := torrentTestBlockedProbe(t)
			s.ffmpegPath = ffmpeg
			s.mediafile = filepath.Join(t.TempDir(), "movie.mp4")
			if err := os.WriteFile(s.mediafile, []byte("media"), 0600); err != nil {
				t.Fatal(err)
			}
			tc.start(s, s.mediafile)
			waitForTorrentTestProbe(t, entered)
			awaitGUIShutdown(t, s)
			// A completed shutdown cannot accept another metadata worker.
			if _, cancel, done, ok := s.workers.begin(context.Background()); ok {
				cancel()
				done()
				t.Fatal("shutdown accepted a new probe")
			}
		})
	}
}

func TestGUIShutdownCancelsLocalStartup(t *testing.T) {
	s := shutdownTestScreen(t)
	ctx, finish, ok := s.beginPlaybackStartup("local.mp4")
	if !ok {
		t.Fatal("startup rejected")
	}
	go func() { <-ctx.Done(); finish() }()
	awaitGUIShutdown(t, s)
	if ctx.Err() == nil {
		t.Fatal("local startup survived shutdown")
	}
	if _, _, ok := s.beginPlaybackStartup("replacement.mp4"); ok {
		t.Fatal("playback accepted after shutdown")
	}
}

func TestGUIShutdownClosesPlaybackServer(t *testing.T) {
	s := shutdownTestScreen(t)
	s.httpserver = httphandlers.NewServer("")
	server := s.httpserver
	entered, exited := make(chan struct{}), make(chan struct{})
	server.AddHandler("/stream", nil, nil, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(exited)
	}))
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.ServeMediaHandler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/stream", nil))
	}()
	<-entered
	awaitGUIShutdown(t, s)
	select {
	case <-exited:
	default:
		t.Fatal("stream survived GUI shutdown")
	}
	<-done
}

func TestRetiredRTMPMonitorCannotStopReplacement(t *testing.T) {
	s := shutdownTestScreen(t)
	old, replacement := rtmp.NewServer(), rtmp.NewServer()
	s.rtmpServer = replacement
	if stopRTMPServerIfCurrent(s, old) || s.rtmpServer != replacement {
		t.Fatal("retired RTMP monitor stopped the replacement server")
	}
}

func TestGUIShutdownWaitsForPendingRTMPStop(t *testing.T) {
	s := shutdownTestScreen(t)
	stopped := make(chan struct{})
	s.rtmpStopDone = stopped
	done := s.beginGUIShutdown()
	select {
	case <-done:
		close(stopped)
		t.Fatal("GUI exited before the pending RTMP stop completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(stopped)
	awaitGUIShutdown(t, s)
}
