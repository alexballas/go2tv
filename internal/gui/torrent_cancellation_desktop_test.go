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

	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/soapcalls"
)

func TestTorrentCancelDuringDLNAStartup(t *testing.T) {
	tt := []struct {
		name   string
		stop   bool
		replay bool
	}{
		{name: "Cancel download"},
		{name: "Stop keeps downloading", stop: true},
		{name: "Play waits for pending Stop", stop: true, replay: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newMediaCardTestScreen(t)
			session, path := newTestTorrentSession(t)
			s.torrent.session, s.torrent.path, s.mediafile = session, path, path
			s.State = "Stopped"
			s.Transcode = true
			s.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
			s.CurrentPos, s.EndPos = binding.NewString(), binding.NewString()
			var plays, stops atomic.Int32
			renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "SUBSCRIBE" {
					w.Header().Set("SID", "uuid:review")
					w.Header().Set("TIMEOUT", "Second-300")
				}
				if strings.Contains(r.Header.Get("SOAPAction"), "#Play\"") {
					plays.Add(1)
				}
				if strings.Contains(r.Header.Get("SOAPAction"), "#Stop\"") {
					stops.Add(1)
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(renderer.Close)
			s.controlURL, s.eventURL = renderer.URL, renderer.URL
			s.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
			t.Cleanup(func() { stopActionSync(s) })
			var entered string
			var release func()
			s.ffmpegPath, entered, release = torrentTestBlockedProbe(t)
			playAction(s)
			waitForTorrentTestProbe(t, entered)
			if tc.replay {
				s.torrent.operationMu.Lock()
			}
			unlock := sync.OnceFunc(func() {
				if tc.replay {
					s.torrent.operationMu.Unlock()
				}
			})
			t.Cleanup(unlock)
			if tc.stop {
				stopAction(s)
			} else {
				cancelTorrent(s)
			}
			done := s.torrentCancellationDone()
			if tc.replay {
				release()
				playAction(s)
				playAction(s)
				time.Sleep(50 * time.Millisecond)
				if plays.Load() != 0 {
					t.Fatal("Play bypassed pending Stop")
				}
				unlock()
			}
			if done != nil {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("cancel blocked")
				}
			}
			release()
			deadline := time.Now().Add(5 * time.Second)
			for {
				s.renderGate.mu.Lock()
				idle := s.renderGate.permits == 0
				s.renderGate.mu.Unlock()
				if idle && (!tc.replay || plays.Load() != 0) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("playback worker stuck")
				}
				time.Sleep(time.Millisecond)
			}
			if tc.replay {
				if plays.Load() != 1 || stops.Load() != 0 || s.httpserver == nil || s.tvdata == nil || s.tvdata.MediaPath != path {
					t.Fatal("pending Stop removed the replay or duplicate Play started another cast")
				}
			} else if plays.Load() != 0 || s.httpserver != nil || s.tvdata != nil {
				t.Fatalf("cancelled startup recreated cast: plays=%d, server=%t, payload=%t, media=%q", plays.Load(), s.httpserver != nil, s.tvdata != nil, s.mediafile)
			}

			if s.hasTorrentSession() != tc.stop {
				t.Fatal("Stop must retain the download; Cancel must close it")
			}
		})
	}
}

func TestTorrentCancelPreservesUnrelatedPlayback(t *testing.T) {
	s, _ := newMediaCardTestScreen(t)
	session, path := newTestTorrentSession(t)
	s.torrent.session, s.torrent.path, s.mediafile = session, path, path
	s.State = "Stopped"
	s.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
	s.CurrentPos, s.EndPos = binding.NewString(), binding.NewString()
	local := filepath.Join(t.TempDir(), "local.mp4")
	if err := os.WriteFile(local, []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	// An independent local cast can coexist with a retained torrent download.
	s.mediafile = local
	var stops atomic.Int32
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("SOAPAction"), "#Stop\"") {
			stops.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(renderer.Close)
	// Represent an established local cast while the torrent still downloads.
	s.tvdata = &soapcalls.TVPayload{ControlURL: renderer.URL, MediaPath: local, MediaType: "video/mp4"}
	s.updateScreenState("Playing")
	cancelTorrent(s)
	if done := s.torrentCancellationDone(); done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("cancel blocked")
		}
	}
	if stops.Load() != 0 || s.getScreenState() != "Playing" || s.tvdata == nil {
		t.Fatalf("cancelling old download stopped local cast: stops=%d, state=%s, payload=%t, selection=%q", stops.Load(), s.getScreenState(), s.tvdata != nil, s.mediafile)
	}
}
