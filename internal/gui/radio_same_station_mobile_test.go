//go:build android || ios

package gui

import (
	"fmt"
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
)

func TestMobileRadioSameStationPreservesStartup(t *testing.T) {
	playAccepted, releasePlay := make(chan struct{}), make(chan struct{})
	finishPlay := sync.OnceFunc(func() { close(releasePlay) })
	defer finishPlay()
	var receiverPlaying atomic.Bool
	var plays, stops atomic.Int32
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "SUBSCRIBE" {
			w.Header().Set("SID", "uuid:radio")
			w.Header().Set("TIMEOUT", "Second-300")
		}
		action := r.Header.Get("SOAPAction")
		if strings.Contains(action, "#Play\"") {
			receiverPlaying.Store(true)
			if plays.Add(1) == 1 {
				close(playAccepted)
			}
			<-releasePlay
		}
		if strings.Contains(action, "#Stop\"") {
			receiverPlaying.Store(false)
			stops.Add(1)
		}
		if strings.Contains(action, "#GetTransportInfo\"") {
			_, _ = fmt.Fprint(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetTransportInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><CurrentTransportState>PLAYING</CurrentTransportState></u:GetTransportInfoResponse></s:Body></s:Envelope>`)
			return
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
	defer func() { finishPlay(); stopActionSync(screen) }()
	station := radiobrowser.Station{UUID: "same", Name: "Same radio", URL: upstream.URL + "/live.m3u8", HLS: 1}
	fyne.DoAndWait(func() { screen.selectRadioStation(station) })
	radioWindow(t.Context(), screen)
	r := screen.radio
	r.client = radioTestDirectory{}
	fyne.DoAndWait(func() {
		r.setStations([]radiobrowser.Station{station})
		r.list.Select(0)
		startMobilePlayback(screen)
	})
	select {
	case <-playAccepted:
	case <-time.After(time.Second):
		t.Fatal("renderer did not accept initial Play")
	}
	fyne.DoAndWait(func() { test.Tap(r.useStation) })
	waitMobileRadioUI(t, func() bool { return !r.selectPending })
	fyne.DoAndWait(func() {
		if !screen.PlayPause.Disabled() || screen.MediaText.Text != station.StreamURL() {
			t.Fatal("same station selection abandoned pending playback")
		}
	})
	finishPlay()
	waitMobileRadioUI(t, func() bool { return screen.getScreenState() == "Playing" && !screen.mobilePlaybackStarting() })
	if !receiverPlaying.Load() || plays.Load() != 1 || stops.Load() != 0 {
		t.Fatal("same station selection interrupted or restarted renderer playback")
	}
	stopActionSync(screen)
	if receiverPlaying.Load() || stops.Load() != 1 {
		t.Fatal("completed radio startup lost receiver Stop control")
	}
}
