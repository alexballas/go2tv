//go:build !(android || ios)

package gui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/internal/mediasource"
)

func TestDesktopReplacementWaitsForTorrentCancellation(t *testing.T) {
	tt := []struct {
		name, source string
		stop         bool
	}{
		{name: "local file", source: "Local File"},
		{name: "URL", source: "URL"},
		{name: "Browse or drop", source: "Browse/drop"},
		{name: "Stop cancels queued Play", source: "Local File", stop: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s, card := newMediaCardTestScreen(t)
			app := fyne.CurrentApp()
			queue := &queuedUIDriver{Driver: app.Driver(), requests: make(chan queuedUIRequest, 128)}
			fyne.SetCurrentApp(&queuedUIApp{App: app, driver: queue})
			t.Cleanup(func() { fyne.SetCurrentApp(app) })
			drainUI := func() {
				for {
					select {
					case request := <-queue.requests:
						runQueuedUI(request)
					default:
						return
					}
				}
			}
			waitFor := func(done <-chan struct{}, failure string) {
				t.Helper()
				timeout := time.NewTimer(5 * time.Second)
				defer timeout.Stop()
				for {
					select {
					case request := <-queue.requests:
						runQueuedUI(request)
					case <-done:
						return
					case <-timeout.C:
						t.Fatal(failure)
					}
				}
			}
			session, path := newTestTorrentSession(t)
			s.torrent.session, s.torrent.path, s.mediafile = session, path, path
			card.refresh()
			s.State = "Stopped"
			s.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
			s.CurrentPos, s.EndPos = binding.NewString(), binding.NewString()

			played, releaseLoad := make(chan struct{}, 4), make(chan struct{})
			finishLoad := sync.OnceFunc(func() { close(releaseLoad) })
			var stops atomic.Int32
			renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "SUBSCRIBE" {
					w.Header().Set("SID", "uuid:replacement")
					w.Header().Set("TIMEOUT", "Second-300")
				}
				if strings.Contains(r.Header.Get("SOAPAction"), "#Play\"") {
					played <- struct{}{}
					<-releaseLoad
				}
				if strings.Contains(r.Header.Get("SOAPAction"), "#Stop\"") {
					stops.Add(1)
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(renderer.Close)
			t.Cleanup(finishLoad)
			s.controlURL, s.eventURL = renderer.URL, renderer.URL
			s.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}

			replacement := torrentTestReplacementImage(t)
			media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.ServeFile(w, r, replacement)
			}))
			t.Cleanup(media.Close)

			// Hold teardown while the user changes source and presses Play twice.
			s.torrent.operationMu.Lock()
			unlock := sync.OnceFunc(s.torrent.operationMu.Unlock)
			t.Cleanup(unlock)
			if tc.source == "Browse/drop" {
				if err := selectMediaPaths(s, []string{replacement}); err != nil {
					t.Fatal(err)
				}
			} else {
				card.source.SetSelected(lang.L(tc.source))
				if tc.source == "URL" {
					s.MediaText.SetText(media.URL + "/replacement.png")
				} else if err := setCurrentMediaPath(s, replacement); err != nil {
					t.Fatal(err)
				}
			}
			if s.torrentCancellationDone() == nil {
				t.Fatal("replacing torrent media did not queue cancellation")
			}
			cancelTorrent(s)
			done := s.torrentCancellationDone()
			if done == nil {
				t.Fatal("source switch did not queue cancellation")
			}
			playAction(s)
			playAction(s)
			setPlayPauseView("Play", s)
			drainUI()
			if !s.PlayPause.Disabled() {
				t.Fatal("cleanup enabled duplicate Play")
			}
			select {
			case <-played:
				t.Fatal("replacement played before torrent teardown")
			case <-time.After(100 * time.Millisecond):
			}
			if tc.stop {
				stopAction(s)
			}
			unlock()
			waitFor(done, "torrent cancellation blocked")
			if s.hasTorrentSession() {
				t.Fatal("old torrent remains selected")
			}
			if _, ok := mediasource.Lookup(path); ok {
				t.Fatal("cancelled torrent source remains registered")
			}
			if _, err := os.Stat(filepath.Dir(filepath.Dir(filepath.Dir(path)))); !os.IsNotExist(err) {
				t.Fatalf("cancelled torrent cache remains: %v", err)
			}
			if tc.stop {
				timeout := time.NewTimer(100 * time.Millisecond)
				defer timeout.Stop()
				for {
					select {
					case request := <-queue.requests:
						runQueuedUI(request)
					case <-played:
						t.Fatal("Stop did not cancel queued Play")
					case <-timeout.C:
						return
					}
				}
			}
			waitFor(played, "replacement did not play after teardown")
			finishLoad()
			// Wait for the playback worker to finish before inspecting its session.
			deadline := time.Now().Add(5 * time.Second)
			for {
				drainUI()
				s.renderGate.mu.Lock()
				idle := s.renderGate.permits == 0
				s.renderGate.mu.Unlock()
				if idle {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("replacement startup blocked")
				}
				time.Sleep(time.Millisecond)
			}
			t.Cleanup(func() { stopActionSync(s); drainUI() })
			if s.tvdata == nil || s.httpserver == nil || s.getScreenState() != "Playing" || stops.Load() != 0 {
				t.Fatal("old torrent cancellation removed replacement playback")
			}
			select {
			case <-played:
				t.Fatal("duplicate Play started a second cast")
			default:
			}
		})
	}
}
