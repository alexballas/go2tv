//go:build android || ios

package gui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/internal/radiobrowser"
)

func TestMobileRadioCancelsQueuedCast(t *testing.T) {
	tt := []struct {
		name        string
		selectRadio bool
	}{
		{name: "Use station", selectRadio: true},
		{name: "Stop before startup registration"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			played := make(chan struct{}, 2)
			renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "SUBSCRIBE" {
					w.Header().Set("SID", "uuid:radio")
					w.Header().Set("TIMEOUT", "Second-300")
				}
				if strings.Contains(r.Header.Get("SOAPAction"), "#Play\"") {
					played <- struct{}{}
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer renderer.Close()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = fmt.Fprintln(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1")
			}))
			defer upstream.Close()
			screen := newMobileTorrentTestScreen(t)
			defer screen.Current.Close()
			app := fyne.CurrentApp()
			fyne.SetCurrentApp(&queuedUIApp{App: app, driver: &serialUIDriver{Driver: app.Driver()}})
			screen.State = "Stopped"
			screen.TranscodeCheckBox = widget.NewCheck("Transcode", nil)
			screen.LoopSelectedCheck = widget.NewCheck("Loop", nil)
			screen.MediaText.OnChanged = screen.invalidateRadioURL
			screen.controlURL, screen.eventlURL = renderer.URL, renderer.URL
			screen.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
			screen.selectedDeviceType = devices.DeviceTypeDLNA
			defer stopActionSync(screen)
			previous := radiobrowser.Station{UUID: "old", Name: "Old radio", URL: upstream.URL + "/old.m3u8", HLS: 1}
			fyne.DoAndWait(func() { screen.selectRadioStation(previous) })
			// A prior teardown blocks Play before a startup context is registered.
			pending := make(chan struct{})
			screen.torrent.mu.Lock()
			screen.torrent.cancelDone = pending
			screen.torrent.mu.Unlock()
			fyne.DoAndWait(func() { startMobilePlayback(screen) })
			screen.torrent.mu.Lock()
			registered := screen.torrent.startup != nil
			screen.torrent.mu.Unlock()
			if registered || !screen.mobilePlaybackStarting() {
				t.Fatal("Play did not wait before startup registration")
			}
			expectedURL := previous.StreamURL()
			if tc.selectRadio {
				radioWindow(t.Context(), screen)
				r := screen.radio
				r.client = radioTestDirectory{}
				station := radiobrowser.Station{UUID: "new", Name: "New radio", URL: upstream.URL + "/live.m3u8", HLS: 1}
				expectedURL = station.StreamURL()
				fyne.DoAndWait(func() {
					r.setStations([]radiobrowser.Station{station})
					r.list.Select(0)
					test.Tap(r.useStation)
				})
			} else {
				fyne.DoAndWait(func() { stopAction(screen) })
			}
			waitMobileRadioUI(t, func() bool {
				return screen.MediaText.Text == expectedURL && !screen.mobilePlaybackStarting()
			})
			screen.torrent.mu.Lock()
			screen.torrent.cancelDone = nil
			screen.torrent.mu.Unlock()
			close(pending)
			select {
			case <-played:
				t.Fatal("canceled Cast played after teardown")
			case <-time.After(100 * time.Millisecond):
			}
			fyne.DoAndWait(func() { startMobilePlayback(screen) })
			select {
			case <-played:
			case <-time.After(time.Second):
				t.Fatal("explicit Play did not cast selected station")
			}
			waitMobileRadioUI(t, func() bool { return !screen.mobilePlaybackStarting() })
		})
	}
}

func waitMobileRadioUI(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		done := false
		fyne.DoAndWait(func() { done = ready() })
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("mobile radio UI did not complete")
}
