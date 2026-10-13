//go:build android || ios

package gui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/soapcalls"
)

func TestMobileRadioStationChangeAndStopWaitForDLNAResume(t *testing.T) {
	tt := []struct {
		name           string
		replaceStation bool
	}{
		{name: "replace station", replaceStation: true},
		{name: "Stop"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			resumeReceived, releaseResume := make(chan struct{}), make(chan struct{})
			finishResume := sync.OnceFunc(func() { close(releaseResume) })
			var receiverPlaying atomic.Bool
			var stops atomic.Int32
			renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				action := r.Header.Get("SOAPAction")
				if strings.Contains(action, "#Play\"") {
					close(resumeReceived)
					<-releaseResume
					receiverPlaying.Store(true)
				}
				if strings.Contains(action, "#Stop\"") {
					receiverPlaying.Store(false)
					stops.Add(1)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer renderer.Close()
			screen := newMobileTorrentTestScreen(t)
			app := fyne.CurrentApp()
			fyne.SetCurrentApp(&queuedUIApp{App: app, driver: &serialUIDriver{Driver: app.Driver()}})
			defer func() {
				finishResume()
				stopActionSync(screen)
				screen.Current.Close()
			}()
			screen.LoopSelectedCheck = widget.NewCheck("Loop", nil)
			screen.TranscodeCheckBox = widget.NewCheck("Transcode", nil)
			screen.MediaText.OnChanged = screen.invalidateRadioURL
			previous := radiobrowser.Station{UUID: "old", Name: "Old radio", URL: "https://old.test/live", Codec: "MP3"}
			fyne.DoAndWait(func() { screen.selectRadioStation(previous) })
			screen.State = "Paused"
			screen.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
			screen.selectedDeviceType = devices.DeviceTypeDLNA
			screen.tvdata = &soapcalls.TVPayload{ControlURL: renderer.URL}
			fyne.DoAndWait(func() { startMobilePlayback(screen) })
			select {
			case <-resumeReceived:
			case <-time.After(time.Second):
				t.Fatal("renderer did not receive resume")
			}
			resumeCtx := screen.playbackStartupContext()
			expectedURL := previous.StreamURL()
			var r *radioView
			if tc.replaceStation {
				radioWindow(t.Context(), screen)
				r = screen.radio
				r.client = radioTestDirectory{}
				station := radiobrowser.Station{UUID: "new", Name: "New radio", URL: "https://new.test/live", Codec: "MP3"}
				expectedURL = station.StreamURL()
				fyne.DoAndWait(func() {
					r.setStations([]radiobrowser.Station{station})
					r.list.Select(0)
					test.Tap(r.useStation)
				})
			} else {
				fyne.DoAndWait(func() { stopAction(screen) })
			}
			select {
			case <-resumeCtx.Done():
			case <-time.After(time.Second):
				t.Fatal("pending resume was not canceled")
			}
			fyne.DoAndWait(func() {
				if stops.Load() != 0 || screen.MediaText.Text != previous.StreamURL() || (r != nil && !r.selectPending) {
					t.Fatal("Stop or station replacement overtook pending resume")
				}
			})
			finishResume()
			waitMobileRadioUI(t, func() bool {
				return stops.Load() == 1 && screen.getScreenState() == "Stopped" && screen.tvdata == nil &&
					screen.MediaText.Text == expectedURL && (r == nil || !r.selectPending) && !screen.playbackStartupPending() && !screen.mobilePlaybackStarting()
			})
			if receiverPlaying.Load() {
				t.Fatal("old radio resumed after final Stop")
			}
		})
	}
}
