//go:build !(android || ios)

package gui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/soapcalls"
)

func newDLNARecoveryScreen(t *testing.T) *FyneScreen {
	t.Helper()
	dir := t.TempDir()
	current, next := filepath.Join(dir, "a.mp4"), filepath.Join(dir, "b.mp4")
	for _, path := range []string{current, next} {
		if err := os.WriteFile(path, []byte("\x00\x00\x00\x18ftypisom\x00\x00\x00\x00isommp42"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := newDLNAQueueStopTestScreen(t, current, next)
	s.Transcode = false
	s.ffmpegPath = filepath.Join(dir, "missing-ffmpeg")
	t.Cleanup(func() { stopActionSync(s) })
	return s
}

func TestDLNACompletionOrdersStopBeforeAutoplay(t *testing.T) {
	for _, cancelNext := range []bool{false, true} {
		name := "advances after cleanup"
		if cancelNext {
			name = "Stop cancels pending autoplay"
		}
		t.Run(name, func(t *testing.T) {
			s := newDLNARecoveryScreen(t)
			stopStarted, releaseStop := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(releaseStop) })
			var firstStop sync.Once
			var loads atomic.Int32
			loaded := make(chan struct{}, 4)
			renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch {
				case r.Method == "SUBSCRIBE":
					w.Header().Set("SID", "uuid:recovery")
					w.Header().Set("TIMEOUT", "Second-300")
				case strings.Contains(r.Header.Get("SOAPAction"), "#Stop\""):
					firstStop.Do(func() { close(stopStarted); <-releaseStop })
				case strings.Contains(r.Header.Get("SOAPAction"), "#SetAVTransportURI\""):
					loads.Add(1)
					loaded <- struct{}{}
				}
			}))
			t.Cleanup(func() { unblock(); stopActionSync(s); renderer.Close() })
			s.tvdata.ControlURL, s.tvdata.EventURL = renderer.URL, renderer.URL
			s.controlURL, s.eventURL = renderer.URL, renderer.URL
			s.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
			s.setActiveDevice(s.selectedDevice)
			s.NextMediaCheck.SetChecked(true)
			session := s.prepareDLNAMonitor(s.serverStopCTX, s.tvdata)
			fyne.DoAndWait(func() { session.complete(playback.MonitorEvent{}, 0); session.complete(playback.MonitorEvent{}, 0) })
			select {
			case <-stopStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("completion did not stop old renderer")
			}
			select {
			case <-loaded:
				t.Fatal("next video loaded before old Stop finished")
			case <-time.After(30 * time.Millisecond):
			}
			if cancelNext {
				fyne.DoAndWait(func() { stopAction(s) })
			}
			unblock()
			if cancelNext {
				if err := s.waitDLNATeardown(t.Context()); err != nil {
					t.Fatal(err)
				}
				fyne.DoAndWait(func() {})
				select {
				case <-loaded:
					t.Fatal("autoplay survived explicit Stop")
				case <-time.After(50 * time.Millisecond):
				}
				return
			}
			select {
			case <-loaded:
			case <-time.After(3 * time.Second):
				t.Fatal("autoplay did not start next video")
			}
			deadline := time.Now().Add(3 * time.Second)
			for s.playbackStartupPending() && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if loads.Load() != 1 {
				t.Fatalf("duplicate completion loaded %d videos", loads.Load())
			}
		})
	}
}

func TestDLNAVerifiedCompletionCleansSubscriptionsBeforeAutoplay(t *testing.T) {
	s := newDLNARecoveryScreen(t)
	unsubscribed, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	var stops atomic.Int32
	loaded := make(chan struct{}, 1)
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch {
		case r.Method == "UNSUBSCRIBE":
			if r.Header.Get("SID") == "uuid:original" {
				close(unsubscribed)
				<-release
			}
		case r.Method == "SUBSCRIBE":
			w.Header().Set("SID", "uuid:next")
			w.Header().Set("TIMEOUT", "Second-300")
		case strings.Contains(r.Header.Get("SOAPAction"), "#Stop\""):
			stops.Add(1)
		case strings.Contains(r.Header.Get("SOAPAction"), "#SetAVTransportURI\""):
			loaded <- struct{}{}
		}
	}))
	t.Cleanup(func() { unblock(); stopActionSync(s); renderer.Close() })
	s.tvdata.ControlURL, s.tvdata.EventURL = renderer.URL, renderer.URL
	s.tvdata.MediaRenderersStates = map[string]*soapcalls.States{"original": {}}
	s.controlURL, s.eventURL = renderer.URL, renderer.URL
	s.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
	s.setActiveDevice(s.selectedDevice)
	s.NextMediaCheck.SetChecked(true)
	s.Debug = newDebugWriter(100)
	s.tvdata.LogOutput = s.Debug
	session := s.prepareDLNAMonitor(s.serverStopCTX, s.tvdata)
	session.handleMonitorEvent(playback.MonitorEvent{Terminal: playback.TerminalFinished, DLNAStopped: true}, 0)
	select {
	case <-unsubscribed:
	case <-time.After(time.Second):
		t.Fatal("verified completion did not unsubscribe original session")
	}
	select {
	case <-loaded:
		t.Fatal("next file loaded before subscription cleanup")
	default:
	}
	unblock()
	select {
	case <-loaded:
	case <-time.After(3 * time.Second):
		t.Fatal("verified completion did not advance")
	}
	if stops.Load() != 0 {
		t.Fatal("redundant Stop sent after verified completion")
	}
}

func TestDLNAStartupFailureRestoresControlsWithoutAutoplay(t *testing.T) {
	s := newDLNARecoveryScreen(t)
	var loads, stops atomic.Int32
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.Contains(r.Header.Get("SOAPAction"), "#SetAVTransportURI\"") {
			loads.Add(1)
		}
		if strings.Contains(r.Header.Get("SOAPAction"), "#Stop\"") {
			stops.Add(1)
		}
	}))
	t.Cleanup(func() { stopActionSync(s); renderer.Close() })
	s.tvdata.ControlURL = renderer.URL
	s.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
	s.Current = fyne.CurrentApp().NewWindow("startup failure")
	t.Cleanup(s.Current.Close)
	s.Debug = newDebugWriter(100)
	s.tvdata.LogOutput = s.Debug
	s.NextMediaCheck.SetChecked(true)
	s.Medialoop = true
	s.updateScreenState("Loading")
	s.mu.Lock()
	_, s.cancelEnablePlay = context.WithCancel(context.Background())
	s.mu.Unlock()
	session := s.prepareDLNAMonitor(s.serverStopCTX, s.tvdata)
	session.handleMonitorEvent(playback.MonitorEvent{Terminal: playback.TerminalError, DLNAStopped: true, Err: errors.New("DLNA playback did not start"), Recovery: "renderer stopped before playback started"}, 0)
	fyne.DoAndWait(func() {})
	if err := s.waitDLNATeardown(t.Context()); err != nil {
		t.Fatal(err)
	}
	fyne.DoAndWait(func() {})
	if s.getScreenState() != "Stopped" || s.dlnaPlaybackPending() || s.PlayPause.Disabled() || !s.ErrorVisible || s.tvdata != nil {
		t.Fatal("failed startup left controls/loading/session stuck")
	}
	if loads.Load() != 0 || stops.Load() != 0 {
		t.Fatalf("startup failure advanced or sent redundant Stop: loads=%d stops=%d", loads.Load(), stops.Load())
	}
}

func TestDLNAUnverifiedHandoffPreservesCurrentRoute(t *testing.T) {
	s := newDLNARecoveryScreen(t)
	current := s.tvdata
	parsed, err := url.Parse(current.MediaURL)
	if err != nil {
		t.Fatal(err)
	}
	s.httpserver.AddHandler(parsed.Path, current, nil, current.MediaPath)
	session := s.prepareDLNAMonitor(s.serverStopCTX, current)
	queued, err := queueNext(s, false)
	if err != nil {
		t.Fatal(err)
	}
	session.handleMonitorEvent(playback.MonitorEvent{NextURIObserved: true}, 0)
	fyne.DoAndWait(func() {})
	if session.current.Load() != current {
		t.Fatal("empty NextURI promoted an unverified track")
	}
	response := httptest.NewRecorder()
	s.httpserver.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, current.MediaURL, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("unverified handoff removed active media: %d", response.Code)
	}
	// Verify the old route retires only after a verified promotion event.
	session.handleMonitorEvent(playback.MonitorEvent{GaplessPromoted: true, GaplessURI: queued.MediaURL}, 0)
	if session.current.Load() != queued {
		t.Fatal("verified queued video did not become current")
	}
	response = httptest.NewRecorder()
	s.httpserver.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, current.MediaURL, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("old route not retired: %d", response.Code)
	}
}

func TestDLNASeekRejectsQueuedCompletion(t *testing.T) {
	s := newDLNARecoveryScreen(t)
	current := s.tvdata
	session := s.prepareDLNAMonitor(s.serverStopCTX, current)
	finishSeek := s.beginDLNASeek(current)
	fyne.DoAndWait(func() {
		session.complete(playback.MonitorEvent{Recovery: "position stalled near end"}, 0)
		session.fail(playback.MonitorEvent{Terminal: playback.TerminalError, Err: errors.New("stale startup timeout")}, 0)
	})
	if s.tvdata != current || session.finished.Load() || s.ErrorVisible {
		t.Fatal("terminal event from before seek stopped playback")
	}
	finishSeek()
}

func TestDLNAQueuedSkipIsCanceledByStop(t *testing.T) {
	s := newDLNARecoveryScreen(t)
	var loads atomic.Int32
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.Contains(r.Header.Get("SOAPAction"), "#SetAVTransportURI\"") {
			loads.Add(1)
		}
	}))
	t.Cleanup(renderer.Close)
	s.tvdata.ControlURL = renderer.URL
	target := playbackTarget{device: devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}, controlURL: renderer.URL, eventURL: renderer.URL}
	queue, _ := s.queueSnapshot()
	next, _ := queue.Item(1)
	fyne.DoAndWait(func() {
		skipToMediaPathOnTargetAction(s, next.Path(), target)
		stopAction(s)
	})
	if err := s.waitDLNATeardown(context.Background()); err != nil {
		t.Fatal(err)
	}
	fyne.DoAndWait(func() {})
	if loads.Load() != 0 || s.playbackStartupPending() {
		t.Fatal("queued skip restarted after Stop")
	}
}

func TestDLNAUIHandoffCanLeaveURLSource(t *testing.T) {
	s := newDLNARecoveryScreen(t)
	queue, _ := s.queueSnapshot()
	next, _ := queue.Item(1)
	fyne.DoAndWait(func() {
		s.ExternalMediaURL.SetChecked(true)
		if err := setCurrentMediaPathOnUI(s, next.Path()); err != nil {
			t.Fatal(err)
		}
		if s.ExternalMediaURL.Checked || s.mediafile != next.Path() {
			t.Fatal("local handoff did not restore local source")
		}
	})
}

// Samsung serves UPnP requests serially; a NOTIFY held open freezes the renderer.
func TestDLNACallbackNeverBlocksRendererNotify(t *testing.T) {
	s := newDLNARecoveryScreen(t)
	session := s.prepareDLNAMonitor(s.serverStopCTX, s.tvdata)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range cap(session.callbacks) + 1 {
			session.HandleCallbackEvent(context.Background(), httphandlers.CallbackEvent{TransportState: "PLAYING"})
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("callback blocked while monitor was not draining events")
	}
}

// Re-sending SetAVTransportURI clears the renderer's staged gapless item.
func TestDLNAStartupReloadRequeuesGaplessItem(t *testing.T) {
	s := newDLNARecoveryScreen(t)
	var mu sync.Mutex
	var actions []string
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		action := r.Header.Get("SOAPAction")
		mu.Lock()
		actions = append(actions, action[strings.LastIndex(action, "#")+1:len(action)-1])
		mu.Unlock()
	}))
	t.Cleanup(func() { stopActionSync(s); renderer.Close() })
	s.tvdata.ControlURL, s.tvdata.EventURL = renderer.URL, renderer.URL
	session := s.prepareDLNAMonitor(s.serverStopCTX, s.tvdata)
	session.queued.Store(&soapcalls.TVPayload{
		ControlURL: renderer.URL, MediaURL: s.tvdata.MediaURL + ".next", MediaType: s.tvdata.MediaType,
		CurrentTimers: map[string]*time.Timer{}, MediaRenderersStates: map[string]*soapcalls.States{}, InitialMediaRenderersStates: map[string]bool{},
	})
	if err := session.RetryStart(t.Context(), s.tvdata.MediaURL, true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(actions, ","); got != "SetAVTransportURI,SetNextAVTransportURI,Play" {
		t.Fatalf("renderer actions = %s", got)
	}
}

// A start that skipped its queue attempt still gets a gapless successor once playing.
func TestDLNAPlayingQueuesMissingGaplessSuccessorOnce(t *testing.T) {
	s := newDLNARecoveryScreen(t)
	var queued atomic.Int32
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.Contains(r.Header.Get("SOAPAction"), "#SetNextAVTransportURI\"") {
			queued.Add(1)
		}
	}))
	t.Cleanup(func() { stopActionSync(s); renderer.Close() })
	s.tvdata.ControlURL, s.tvdata.EventURL = renderer.URL, renderer.URL
	s.NextMediaCheck.SetChecked(true)
	previous := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")
	fyne.CurrentApp().Preferences().SetString("Gapless", "Enabled")
	t.Cleanup(func() { fyne.CurrentApp().Preferences().SetString("Gapless", previous) })
	session := s.prepareDLNAMonitor(s.serverStopCTX, s.tvdata)
	for range 3 {
		session.handleMonitorEvent(playback.MonitorEvent{State: "PLAYING"}, 0)
	}
	deadline := time.Now().Add(3 * time.Second)
	for queued.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	session.handleMonitorEvent(playback.MonitorEvent{State: "PLAYING"}, 0)
	time.Sleep(200 * time.Millisecond)
	if got := queued.Load(); got != 1 {
		t.Fatalf("SetNextAVTransportURI sent %d times, want once", got)
	}
}
