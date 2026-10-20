//go:build !(android || ios)

package gui

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/soapcalls"
)

func waitUI(t *testing.T, timeout time.Duration, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		done := false
		fyne.DoAndWait(func() { done = ready() })
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("UI did not reach expected state")
}

// dlnaTestRenderer records SOAP actions and reports transportState to polls.
func dlnaTestRenderer(t *testing.T, transportState string) (*httptest.Server, chan string) {
	t.Helper()
	actions := make(chan string, 32)
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Method == "SUBSCRIBE" {
			w.Header().Set("SID", "uuid:test")
			w.Header().Set("TIMEOUT", "Second-300")
		}
		action := r.Header.Get("SOAPAction")
		// Status polls would crowd out the control actions under test.
		if _, name, found := strings.Cut(action, "#"); found && !strings.Contains(action, "#GetTransportInfo\"") {
			select {
			case actions <- strings.Trim(name, `"`):
			default:
			}
		}
		if strings.Contains(action, "#GetTransportInfo\"") {
			_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetTransportInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><CurrentTransportState>`+transportState+`</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus><CurrentSpeed>1</CurrentSpeed></u:GetTransportInfoResponse></s:Body></s:Envelope>`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(renderer.Close)
	return renderer, actions
}

func newRadioPlaybackTestScreen(t *testing.T) (*FyneScreen, *mediaSelectionCard) {
	t.Helper()
	s, card := newMediaCardTestScreen(t)
	s.Current = fyne.CurrentApp().NewWindow("Radio")
	t.Cleanup(s.Current.Close)
	s.Current.SetContent(card.content)
	s.TranscodeCheckBox = widget.NewCheck("", nil)
	s.ffmpegPath = filepath.Join(t.TempDir(), "missing-ffmpeg")
	s.playbackStatus = newPlaybackStatusLabel("")
	s.Stop = widget.NewButton("Stop", func() { stopAction(s) })
	s.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
	s.CurrentPos, s.EndPos = binding.NewString(), binding.NewString()
	s.torrent.ctx = t.Context()
	return s, card
}

func TestRadioSwitchContinuesCastOnlyWhenPlaying(t *testing.T) {
	tt := []struct {
		name    string
		playing bool
	}{
		{name: "playing switches station", playing: true},
		{name: "stopped only selects station"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s, card := newRadioPlaybackTestScreen(t)
			renderer, actions := dlnaTestRenderer(t, "PLAYING")
			s.controlURL, s.eventURL = renderer.URL, renderer.URL
			s.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
			station := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "audio/mpeg")
				_, _ = io.WriteString(w, "audio")
			}))
			t.Cleanup(station.Close)
			s.ExternalMediaURL.SetChecked(true)
			s.MediaText.SetText("https://station.test/old")
			if tc.playing {
				s.State = "Playing"
				s.tvdata = &soapcalls.TVPayload{ControlURL: renderer.URL}
			}
			t.Cleanup(func() { stopActionSync(s) })
			radioWindow(t.Context(), s)
			r := s.radio
			next := radiobrowser.Station{UUID: "next", Name: "Next", URL: station.URL + "/live"}
			r.client = radioTestDirectory{stations: []radiobrowser.Station{next}}
			fyne.DoAndWait(func() { test.Tap(card.radio) })
			waitRadioUI(t, func() bool { return r.list.Length() == 1 })
			fyne.DoAndWait(func() { r.list.Select(0); test.Tap(r.useStation) })
			waitRadioUI(t, func() bool { return s.MediaText.Text == next.StreamURL() && r.modal == nil })
			var seen []string
			timeout := time.After(5 * time.Second)
			if !tc.playing {
				timeout = time.After(300 * time.Millisecond)
			}
		collect:
			for {
				select {
				case action := <-actions:
					seen = append(seen, action)
					if action == "SetAVTransportURI" {
						break collect
					}
				case <-timeout:
					break collect
				}
			}
			loaded := strings.Contains(strings.Join(seen, ","), "SetAVTransportURI")
			if loaded != tc.playing {
				t.Fatalf("renderer actions after selection = %v", seen)
			}
			if tc.playing && seen[0] != "Stop" {
				t.Fatalf("new station loaded before stopping the old one: %v", seen)
			}
		})
	}
}

func TestDLNAStartupGivesUpWhenRendererNeverPlays(t *testing.T) {
	tt := []struct {
		name, transportState string
	}{
		{name: "renderer settles in STOPPED", transportState: "STOPPED"},
		{name: "renderer never leaves TRANSITIONING", transportState: "TRANSITIONING"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newRadioPlaybackTestScreen(t)
			renderer, actions := dlnaTestRenderer(t, tc.transportState)
			s.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
			s.setActiveDevice(s.selectedDevice)
			s.tvdata = &soapcalls.TVPayload{ControlURL: renderer.URL}
			t.Cleanup(func() { stopActionSync(s) })
			s.watchDLNAStartup(s.tvdata, dlnaStartupWait{interval: 5 * time.Millisecond, timeout: 300 * time.Millisecond, stoppedPolls: 3})
			waitUI(t, 2*time.Second, func() bool { return !s.dlnaPlaybackPending() && s.ErrorVisible })
			fyne.DoAndWait(func() {
				if s.getScreenState() != "Stopped" || s.getActiveDevice().addr != "" {
					t.Fatalf("failed startup left state %q on %q", s.getScreenState(), s.getActiveDevice().addr)
				}
			})
			deadline := time.After(time.Second)
			for {
				select {
				case action := <-actions:
					if action == "Stop" {
						return
					}
				case <-deadline:
					t.Fatal("renderer was not stopped after failed startup")
				}
			}
		})
	}
}

func TestRadioRelayTitlesAvoidSecondStationConnection(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	screen, _ := newMediaCardTestScreen(t)
	status := newPlaybackStatusLabel("")
	screen.playbackStatus = status
	screen.State = "Playing"
	screen.setPlayingMediaPath(server.URL)
	screen.radioSelected = radiobrowser.Station{UUID: "one", Name: "Station", URL: server.URL}
	onTitle := screen.radioRelayTitles(server.URL)
	// The renderer may connect, and the first title arrive, before Play returns.
	onTitle("Early - Song")
	screen.startRadioSongMonitor(server.URL)
	defer screen.stopRadioSongMonitor()
	waitRadioUI(t, func() bool { return strings.Contains(status.Text, "Early - Song") })
	onTitle("Next - Song")
	waitRadioUI(t, func() bool { return strings.Contains(status.Text, "Next - Song") })
	fyne.DoAndWait(func() { screen.updateScreenState("Stopped") })
	onTitle("Late - Song")
	fyne.DoAndWait(func() {
		screen.State = "Playing"
		if strings.Contains(status.Text, "Late - Song") {
			t.Fatal("stopped relay updated the title")
		}
	})
	if requests.Load() != 0 {
		t.Fatalf("relayed station opened %d extra connections", requests.Load())
	}
}

func TestRadioRowTapSelectsAtOnceAndDoubleTapOrEnterUseStation(t *testing.T) {
	row := func(t *testing.T, r *radioView, id widget.ListItemID) *radioStationRow {
		for _, object := range test.LaidOutObjects(r.list) {
			if row, ok := object.(*radioStationRow); ok && row.id == id {
				return row
			}
		}
		t.Fatal("station row not rendered")
		return nil
	}
	tt := []struct {
		name string
		use  func(t *testing.T, r *radioView)
	}{
		{name: "double-tap", use: func(t *testing.T, r *radioView) {
			test.Tap(row(t, r, 1))
			test.Tap(row(t, r, 1))
		}},
		{name: "Enter", use: func(t *testing.T, r *radioView) { r.list.OnReturn(1) }},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s, card := newRadioPlaybackTestScreen(t)
			s.Current.Resize(fyne.NewSize(620, 560))
			radioWindow(t.Context(), s)
			r := s.radio
			stations := []radiobrowser.Station{
				{UUID: "one", Name: "One", URL: "https://station.test/one"},
				{UUID: "two", Name: "Two", URL: "https://station.test/two"},
			}
			r.client = radioTestDirectory{stations: stations}
			fyne.DoAndWait(func() { test.Tap(card.radio) })
			waitRadioUI(t, func() bool { return r.list.Length() == 2 })
			fyne.DoAndWait(func() {
				// Single taps must not wait for a possible double-tap.
				test.Tap(row(t, r, 0))
				if r.selected != 0 || r.useStation.Disabled() || r.modal == nil {
					t.Fatal("single tap did not select immediately, or used the station")
				}
				tc.use(t, r)
			})
			waitRadioUI(t, func() bool { return s.MediaText.Text == stations[1].StreamURL() && r.modal == nil })
		})
	}
}

func TestRadioRowSummaryShowsLeadingUniqueTags(t *testing.T) {
	station := radiobrowser.Station{Country: "Greece", Codec: "MP3", Bitrate: 128, Tags: "jazz, Jazz,,soul ,funk,blues"}
	if got := radioStationSummary(station); got != "Greece · MP3 · 128 kbps · jazz, soul, funk" {
		t.Fatalf("row summary = %q", got)
	}
}

func TestRadioRecentKeepsLatestStationsFirst(t *testing.T) {
	s, _ := newRadioPlaybackTestScreen(t)
	radioWindow(t.Context(), s)
	r := s.radio
	fyne.DoAndWait(func() {
		for i := range maxRadioRecent + 5 {
			r.recordRecent(radiobrowser.Station{UUID: fmt.Sprint(i), Name: fmt.Sprint(i)})
		}
		r.recordRecent(radiobrowser.Station{UUID: "10", Name: "replayed"})
	})
	s.radio = nil
	radioWindow(t.Context(), s)
	restored := s.radio
	fyne.DoAndWait(func() {
		restored.showRecent()
		if len(restored.stations) != maxRadioRecent || restored.stations[0].Name != "replayed" || restored.stations[1].UUID != fmt.Sprint(maxRadioRecent+4) {
			t.Fatalf("recent stations = %+v", restored.stations[:2])
		}
		count := 0
		for _, station := range restored.stations {
			if station.UUID == "10" {
				count++
			}
		}
		if count != 1 {
			t.Fatal("replayed station listed twice")
		}
	})
}

func TestRadioCountryNamesFollowAppLanguage(t *testing.T) {
	newMediaCardTestScreen(t)
	tt := []struct{ language, want string }{
		{language: "English", want: "Germany"},
		{language: "中文(简体)", want: "德国"},
	}
	for _, tc := range tt {
		t.Run(tc.language, func(t *testing.T) {
			fyne.CurrentApp().Preferences().SetString("Language", tc.language)
			if got := radioCountryName("DE"); got != tc.want {
				t.Fatalf("country name = %q", got)
			}
		})
	}
}
