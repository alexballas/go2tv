//go:build android || ios

package gui

import (
	"context"
	"io"
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
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/internal/mediasource"
)

type unavailableMobileTorrentSource struct{ mediasource.Source }

func (unavailableMobileTorrentSource) MIME() string { return "video/mp4" }
func (unavailableMobileTorrentSource) Open(context.Context) (io.ReadSeekCloser, error) {
	return nil, os.ErrNotExist
}

func newMobileTorrentTestScreen(t *testing.T) *FyneScreen {
	t.Helper()
	app := test.NewApp()
	t.Cleanup(app.Quit)
	return &FyneScreen{
		Current:   app.NewWindow("Torrent"),
		MediaText: widget.NewEntry(), SubsText: widget.NewEntry(),
		PlayPause:        widget.NewButton("Play", nil),
		ExternalMediaURL: widget.NewCheck("", nil),
		CurrentPos:       binding.NewString(), EndPos: binding.NewString(),
		SlideBar: &tappedSlider{Slider: widget.NewSlider(0, 100)},
	}
}

func selectMobileTestTorrent(t *testing.T, s *FyneScreen) {
	t.Helper()
	session, path := newTestTorrentSession(t)
	s.torrent.session, s.torrent.path = session, path
	s.mediafile = storage.NewFileURI(path)
}

func TestMobileReplacementWaitsForTorrentCancellation(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	selectMobileTestTorrent(t, s)

	played := make(chan struct{}, 1)
	releaseLoad := make(chan struct{})
	finishLoad := sync.OnceFunc(func() { close(releaseLoad) })
	defer finishLoad()
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "SUBSCRIBE" {
			w.Header().Set("SID", "uuid:replacement")
			w.Header().Set("TIMEOUT", "Second-300")
		}
		if strings.Contains(r.Header.Get("SOAPAction"), "#Play\"") {
			played <- struct{}{}
			<-releaseLoad
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(renderer.Close)
	s.controlURL, s.eventlURL = renderer.URL, renderer.URL

	replacement := torrentTestReplacementImage(t)
	// Hold cancellation until Play has had a chance to run. A second cancel
	// must not let playback bypass the first queued teardown.
	s.torrent.operationMu.Lock()
	setMobileMediaURI(s, storage.NewFileURI(replacement))
	cancelTorrent(s)
	if !claimMobilePlayback(s) {
		t.Fatal("first Play rejected")
	}
	for range 3 {
		if claimMobilePlayback(s) {
			t.Fatal("duplicate Play accepted during cancellation")
		}
	}
	setPlayPauseView("Play", s)
	if !s.PlayPause.Disabled() {
		t.Fatal("teardown re-enabled Play during pending startup")
	}
	playDone := make(chan struct{})
	go func() {
		playMobileAction(s)
		close(playDone)
	}()
	premature := false
	select {
	case <-playDone:
		premature = true
	case <-time.After(100 * time.Millisecond):
	}
	s.torrent.operationMu.Unlock()
	select {
	case <-played:
		if claimMobilePlayback(s) {
			t.Fatal("duplicate Play accepted while renderer load was pending")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement did not reach renderer after cancellation")
	}
	finishLoad()
	select {
	case <-playDone:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement playback blocked after cancellation")
	}
	if done := s.torrentCancellationDone(); done != nil {
		<-done
	}
	t.Cleanup(func() { stopActionSync(s) })
	if premature {
		t.Fatal("replacement playback ran before torrent cancellation")
	}
	if s.tvdata == nil || s.httpserver == nil || s.hasTorrentSession() {
		t.Fatal("torrent cancellation removed the replacement cast or retained the torrent")
	}
	if s.mediafile == nil || s.mediafile.Path() != replacement {
		t.Fatal("torrent cancellation cleared replacement media")
	}
	if s.mobilePlaybackStarting() {
		t.Fatal("completed playback retained startup guard")
	}
}

func TestMobileTorrentCancelDuringDLNAStartup(t *testing.T) {
	tt := []struct {
		name string
		stop bool
	}{
		{name: "Cancel download"},
		{name: "Stop keeps downloading", stop: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s := newMobileTorrentTestScreen(t)
			selectMobileTestTorrent(t, s)
			s.State, s.Transcode = "Stopped", true
			var plays atomic.Int32
			renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "SUBSCRIBE" {
					w.Header().Set("SID", "uuid:cancel")
					w.Header().Set("TIMEOUT", "Second-300")
				}
				if strings.Contains(r.Header.Get("SOAPAction"), "#Play\"") {
					plays.Add(1)
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(renderer.Close)
			s.controlURL, s.eventlURL = renderer.URL, renderer.URL
			s.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
			t.Cleanup(func() { stopActionSync(s) })
			var entered string
			var release func()
			s.ffmpegPath, entered, release = torrentTestBlockedProbe(t)
			if !claimMobilePlayback(s) {
				t.Fatal("first Play rejected")
			}
			playDone := make(chan struct{})
			go func() { playMobileAction(s); close(playDone) }()
			waitForTorrentTestProbe(t, entered)
			if tc.stop {
				stopAction(s)
			} else {
				cancelTorrent(s)
			}
			if done := s.torrentCancellationDone(); done != nil {
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("cancel did not interrupt startup")
				}
			}
			release()
			select {
			case <-playDone:
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled playback worker remains running")
			}
			if plays.Load() != 0 || s.httpserver != nil || s.tvdata != nil || s.mobilePlaybackStarting() {
				t.Fatal("cancelled startup recreated a cast or retained startup/download state")
			}

			if s.hasTorrentSession() != tc.stop {
				t.Fatal("Stop must retain the download; Cancel must close it")
			}
		})
	}
}

func TestMobileTorrentUsesProgressiveSource(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	path := filepath.Join(t.TempDir(), "movie.mp4")
	t.Cleanup(mediasource.Register(path, unavailableMobileTorrentSource{}))
	s.mediafile = storage.NewFileURI(path)
	// No backing file or available pieces: selection and MIME must not read it.
	mime, err := mobileMediaMIME(s.mediafile)
	if err != nil || mime != "video/mp4" {
		t.Fatalf("MIME = %q, err = %v", mime, err)
	}
	media, err := seekableMediaForCasting(s)
	if err != nil || media != path || s.tempMediaFile != "" {
		t.Fatalf("media = %v, err = %v, temp = %q", media, err, s.tempMediaFile)
	}
	media, err = mobileMediaForTranscodedSeek(s)
	if err != nil || media != path {
		t.Fatalf("seek media = %v, err = %v", media, err)
	}
}

func TestMobileTorrentSelectionClearsSubtitles(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	s.subsfile = storage.NewFileURI("/old.srt")
	s.SubsText.SetText("old.srt")
	path := filepath.Join(t.TempDir(), "subs.srt")
	if err := os.WriteFile(path, []byte("old subtitles"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.tempSubsFile = path
	mediaPath := filepath.Join(t.TempDir(), "movie.mp4")
	selectTorrentMedia(s, mediaPath)
	if s.subsfile != nil || s.SubsText.Text != "" || s.tempSubsFile != "" {
		t.Fatal("torrent selection retained previous subtitles")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary subtitles retained: %v", err)
	}
	if s.mediafile.Path() != mediaPath || s.MediaText.Text != "movie.mp4" {
		t.Fatal("torrent media selection missing")
	}
}

func TestMobileClearTorrentSelection(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	path := filepath.Join(t.TempDir(), "movie.mp4")
	t.Cleanup(mediasource.Register(path, unavailableMobileTorrentSource{}))
	s.mediafile = storage.NewFileURI(path)
	s.MediaText.SetText("movie.mp4")
	clearmediaAction(s)
	if s.mediafile != nil || s.MediaText.Text != "" {
		t.Fatal("clear retained the torrent URI while cancellation ran")
	}
	s.mediafile = storage.NewFileURI("/replacement.mp4")
	s.MediaText.SetText("replacement.mp4")
	clearTorrentSelection(s, path)
	if s.MediaText.Text != "replacement.mp4" {
		t.Fatal("torrent cleanup cleared replacement media")
	}
}
