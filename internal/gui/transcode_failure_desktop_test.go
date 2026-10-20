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

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/utils"
)

func failedTranscodeTestServer(t *testing.T) *httphandlers.HTTPserver {
	t.Helper()
	media := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(media, []byte("input"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httphandlers.NewServer("")
	t.Cleanup(server.StopServer)
	server.AddHandler("/movie.mkv", nil, &utils.TranscodeOptions{FFmpegPath: filepath.Join(t.TempDir(), "missing-ffmpeg")}, media)
	server.ServeMediaHandler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/movie.mkv", nil))
	return server
}

func TestTranscodeFailureResetsOnlyCurrentPlayback(t *testing.T) {
	tt := []struct {
		name        string
		replacement string
	}{
		{name: "current encoder failure"},
		{name: "late error from replaced server", replacement: "server"},
		{name: "queued error from previous action on same server", replacement: "action"},
		{name: "queued error from retired route", replacement: "route"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			screen, _ := newMediaCardTestScreen(t)
			screen.Current = fyne.CurrentApp().NewWindow("test")
			screen.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
			screen.CurrentPos, screen.EndPos = binding.NewString(), binding.NewString()
			screen.State = "Playing"
			screen.activeDevice = devType{addr: "http://living-room:8009", deviceType: devices.DeviceTypeChromecast}
			screen.chromecastClient = newConnectedCastClientForTest(t, screen.activeDevice.addr)
			failed := failedTranscodeTestServer(t)
			screen.httpserver = failed
			actionID := screen.nextChromecastActionID()
			app := fyne.CurrentApp()
			queue := &queuedUIDriver{Driver: app.Driver(), requests: make(chan queuedUIRequest, 1)}
			fyne.SetCurrentApp(&queuedUIApp{App: app, driver: queue})
			if !stopFailedTranscode(screen, failed, nil, actionID) {
				t.Fatal("encoder error was not handled")
			}
			fyne.SetCurrentApp(app)
			switch tc.replacement {
			case "server":
				screen.httpserver = httphandlers.NewServer("")
				t.Cleanup(screen.httpserver.StopServer)
			case "action":
				screen.nextChromecastActionID()
			case "route":
				failed.RemoveHandler("/movie.mkv")
				failed.AddHandler("/audio.mp3", nil, nil, []byte("audio"))
			}
			runQueuedUI(<-queue.requests)
			fyne.DoAndWait(func() {})
			want := "Stopped"
			if tc.replacement != "" {
				want = "Playing"
			}
			if state := screen.getScreenState(); state != want {
				t.Fatalf("playback state = %q, want %q", state, want)
			}
			if tc.replacement == "" && (screen.chromecastClient != nil || screen.httpserver != nil || screen.getActiveDevice().addr != "") {
				t.Fatal("failed transcode retained an active playback session")
			}
		})
	}
}

func TestChromecastWatcherKeepsPollingWhileFailureCallbackIsPending(t *testing.T) {
	screen, _ := newMediaCardTestScreen(t)
	screen.State = "Playing"
	screen.chromecastClient = newConnectedCastClientForTest(t, "http://living-room:8009")
	screen.httpserver = failedTranscodeTestServer(t)
	actionID := screen.nextChromecastActionID()
	app := fyne.CurrentApp()
	queue := &queuedUIDriver{Driver: app.Driver(), requests: make(chan queuedUIRequest, 4)}
	fyne.SetCurrentApp(&queuedUIApp{App: app, driver: queue})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		chromecastStatusWatcher(ctx, screen, actionID)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		fyne.SetCurrentApp(app)
	})
	// Two polls with the UI callback still queued prove that a receiver retry
	// can resume tracking, rather than losing its only status watcher.
	var pending queuedUIRequest
	for range 2 {
		select {
		case pending = <-queue.requests:
		case <-done:
			t.Fatal("status watcher exited before failure callback ran")
		case <-ctx.Done():
			t.Fatal("status watcher did not keep polling")
		}
	}
	cancel()
	<-done
	fyne.SetCurrentApp(app)
	// A fresh request/media replacement clears the error on the same server
	// and action before the queued callback executes.
	screen.httpserver.AddHandler("/movie.mkv", nil, nil, []byte("retry"))
	runQueuedUI(pending)
	if state := screen.getScreenState(); state != "Playing" {
		t.Fatalf("queued obsolete failure stopped retry: %q", state)
	}
}
