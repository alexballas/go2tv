//go:build !(android || ios)

package gui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/soapcalls"
)

func TestRadioModalSelectsURLBeforeDeviceSelection(t *testing.T) {
	screen, card := newMediaCardTestScreen(t)
	screen.Current = fyne.CurrentApp().NewWindow("Radio")
	defer screen.Current.Close()
	screen.Current.SetContent(card.content)
	screen.Current.Resize(fyne.NewSize(640, 600))
	screen.State = "Stopped"
	screen.TranscodeCheckBox = widget.NewCheck("Transcode", func(value bool) { screen.Transcode = value })
	screen.TranscodeCheckBox.SetChecked(true)
	ctx := t.Context()
	screen.torrent.ctx = ctx
	radioWindow(ctx, screen)
	r := screen.radio
	station := radiobrowser.Station{UUID: "station", Name: "Station", URL: "https://station.test/live.m3u8", HLS: 1}
	r.client = radioTestDirectory{stations: []radiobrowser.Station{station}, countries: []radiobrowser.Country{{Name: "Greece", Code: "GR"}}}
	if !card.radio.Visible() || card.source.Selected != lang.L("Local File") {
		t.Fatal("Radio launcher unavailable in Local File mode")
	}
	test.Tap(card.radio)
	if r.modal == nil {
		t.Fatal("Radio launcher did not open a modal")
	}
	defer func() {
		if r.modal != nil {
			r.modal.Hide()
		}
	}()
	waitRadioUI(t, func() bool { return r.list.Length() == 1 })
	fyne.DoAndWait(func() { r.list.Select(0); test.Tap(r.useStation) })
	waitRadioUI(t, func() bool { return screen.MediaText.Text == station.StreamURL() && r.modal == nil })
	fyne.DoAndWait(func() {
		if screen.MediaText.Text != station.StreamURL() || !screen.ExternalMediaURL.Checked || card.source.Selected != lang.L("URL") {
			t.Fatal("radio selection did not switch to URL and populate the station")
		}
		if r.modal != nil {
			t.Fatal("station selector stayed open")
		}
		if screen.selectedDevice.addr != "" || screen.getScreenState() != "Stopped" || screen.tvdata != nil || screen.chromecastClient != nil || screen.httpserver != nil {
			t.Fatal("URL selection started playback or required a device")
		}
		if !screen.TranscodeCheckBox.Checked || !screen.Transcode {
			t.Fatal("radio selection lost the main transcode setting")
		}
		if title := screen.radioTitle(station.StreamURL()); title != station.Name {
			t.Fatalf("station metadata lost: %q", title)
		}
	})
}

func waitRadioUI(t *testing.T, ready func() bool) {
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
	t.Fatal("radio UI did not complete")
}

func TestRadioSelectionReleasesReplacedTorrent(t *testing.T) {
	screen, card := newMediaCardTestScreen(t)
	screen.Current = fyne.CurrentApp().NewWindow("Radio")
	defer screen.Current.Close()
	screen.Current.SetContent(card.content)
	screen.State = "Stopped"
	screen.TranscodeCheckBox = widget.NewCheck("Transcode", nil)
	session, path := newTestTorrentSession(t)
	screen.torrent.session, screen.torrent.path, screen.mediafile = session, path, path
	card.refresh()
	radioWindow(t.Context(), screen)
	r := screen.radio
	station := radiobrowser.Station{UUID: "radio", Name: "Radio", URL: "https://station.test/live"}
	// Keep teardown pending while the new selection is installed.
	screen.torrent.operationMu.Lock()
	unlock := sync.OnceFunc(screen.torrent.operationMu.Unlock)
	defer unlock()
	fyne.DoAndWait(func() {
		r.setStations([]radiobrowser.Station{station})
		r.list.Select(0)
		test.Tap(r.useStation)
	})
	waitRadioUI(t, func() bool { return screen.MediaText.Text == station.StreamURL() })
	done := screen.torrentCancellationDone()
	if done == nil {
		t.Fatal("selecting radio did not cancel the replaced torrent")
	}
	unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("radio source switch did not finish torrent cleanup")
	}
	if screen.hasTorrentSession() {
		t.Fatal("radio source switch retained the torrent download")
	}
	if _, exists := mediasource.Lookup(path); exists {
		t.Fatal("radio source switch retained the torrent source")
	}
	fyne.DoAndWait(func() {
		if !screen.ExternalMediaURL.Checked || screen.mediafile != station.StreamURL() || screen.MediaText.Text != station.StreamURL() || screen.radioTitle(station.StreamURL()) != station.Name {
			t.Fatal("torrent cleanup removed the radio selection")
		}
		card.source.SetSelected(lang.L("Local File"))
		if screen.mediafile != "" {
			t.Fatal("returning to local mode restored the released torrent")
		}
	})
}

func TestRadioSelectionDoesNotWaitForUnavailableFavicon(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer blocked.Close()
	screen, card := newMediaCardTestScreen(t)
	screen.Current = fyne.CurrentApp().NewWindow("Radio")
	defer screen.Current.Close()
	screen.Current.SetContent(card.content)
	screen.TranscodeCheckBox = widget.NewCheck("Transcode", nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	screen.torrent.ctx = ctx
	radioWindow(ctx, screen)
	r := screen.radio
	station := radiobrowser.Station{UUID: "station", Name: "Station", URL: "https://station.test/live", Favicon: blocked.URL}
	r.client = radioTestDirectory{stations: []radiobrowser.Station{station}}
	test.Tap(card.radio)
	defer func() {
		if r.modal != nil {
			r.modal.Hide()
		}
	}()
	waitRadioUI(t, func() bool { return r.list.Length() == 1 })
	fyne.DoAndWait(func() { r.list.Select(0); test.Tap(r.useStation) })
	waitRadioUI(t, func() bool { return screen.MediaText.Text == station.StreamURL() && r.modal == nil })
	cancel()
}

func TestRadioSelectionCancelsQueuedCast(t *testing.T) {
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
	screen, _ := newMediaCardTestScreen(t)
	screen.Current = fyne.CurrentApp().NewWindow("Radio")
	defer screen.Current.Close()
	screen.State = "Stopped"
	screen.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
	screen.CurrentPos, screen.EndPos = binding.NewString(), binding.NewString()
	screen.TranscodeCheckBox = widget.NewCheck("Transcode", nil)
	screen.controlURL, screen.eventURL = renderer.URL, renderer.URL
	screen.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
	screen.selectedDeviceType = devices.DeviceTypeDLNA
	defer stopActionSync(screen)
	// Hold a teardown barrier so the user's first Cast queues a continuation.
	pending := make(chan struct{})
	screen.torrent.cancelDone = pending
	fyne.DoAndWait(func() { playAction(screen) })
	if !screen.torrentPlaybackPending() {
		t.Fatal("Cast did not wait for teardown")
	}
	radioWindow(t.Context(), screen)
	r := screen.radio
	r.client = radioTestDirectory{}
	station := radiobrowser.Station{UUID: "radio", Name: "Radio", URL: upstream.URL + "/live.m3u8", HLS: 1}
	fyne.DoAndWait(func() {
		r.setStations([]radiobrowser.Station{station})
		r.list.Select(0)
		test.Tap(r.useStation)
	})
	waitRadioUI(t, func() bool { return screen.MediaText.Text == station.StreamURL() })
	if screen.torrentPlaybackPending() {
		t.Fatal("station selection retained the previous queued Cast")
	}
	fyne.DoAndWait(func() { screen.torrent.cancelDone = nil })
	close(pending)
	select {
	case <-played:
		t.Fatal("Use station cast without another Play press")
	case <-time.After(100 * time.Millisecond):
	}
	fyne.DoAndWait(func() { playAction(screen) })
	select {
	case <-played:
	case <-time.After(time.Second):
		t.Fatal("explicit Play did not cast the selected station")
	}
}

func TestRadioExplicitCastSurvivesTorrentCleanup(t *testing.T) {
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
	screen, card := newMediaCardTestScreen(t)
	screen.Current = fyne.CurrentApp().NewWindow("Radio")
	defer screen.Current.Close()
	screen.State = "Stopped"
	screen.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
	screen.CurrentPos, screen.EndPos = binding.NewString(), binding.NewString()
	screen.TranscodeCheckBox = widget.NewCheck("Transcode", nil)
	screen.controlURL, screen.eventURL = renderer.URL, renderer.URL
	screen.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
	screen.selectedDeviceType = devices.DeviceTypeDLNA
	defer stopActionSync(screen)
	session, path := newTestTorrentSession(t)
	screen.torrent.session, screen.torrent.path, screen.mediafile = session, path, path
	// Teardown still owns the previous torrent until its cleanup finishes.
	screen.torrent.playback = session
	card.refresh()
	screen.torrent.operationMu.Lock()
	unlock := sync.OnceFunc(screen.torrent.operationMu.Unlock)
	defer unlock()
	radioWindow(t.Context(), screen)
	r := screen.radio
	r.client = radioTestDirectory{}
	station := radiobrowser.Station{UUID: "radio", Name: "Radio", URL: upstream.URL + "/live.m3u8", HLS: 1}
	fyne.DoAndWait(func() {
		r.setStations([]radiobrowser.Station{station})
		r.list.Select(0)
		test.Tap(r.useStation)
	})
	waitRadioUI(t, func() bool { return screen.MediaText.Text == station.StreamURL() })
	done := screen.torrentCancellationDone()
	if done == nil {
		t.Fatal("station selection did not queue torrent cleanup")
	}
	fyne.DoAndWait(func() { playAction(screen) })
	if !screen.torrentPlaybackPending() {
		t.Fatal("explicit radio Cast did not wait for cleanup")
	}
	unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("torrent cleanup did not finish")
	}
	select {
	case <-played:
	case <-time.After(time.Second):
		t.Fatal("torrent cleanup discarded explicit Cast for selected radio")
	}
}

func TestRadioSelectionStopsOldPlaybackBeforeReplacingURL(t *testing.T) {
	screen, card := newMediaCardTestScreen(t)
	screen.Current = fyne.CurrentApp().NewWindow("Radio")
	defer screen.Current.Close()
	screen.Current.SetContent(card.content)
	screen.TranscodeCheckBox = widget.NewCheck("Transcode", nil)
	screen.ExternalMediaURL.SetChecked(true)
	previousURL := "https://station.test/old"
	screen.MediaText.SetText(previousURL)
	screen.State = "Playing"
	stopped := make(chan string, 1)
	previousReceiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("SOAPAction"), "Stop") {
			fyne.DoAndWait(func() { stopped <- screen.MediaText.Text })
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer previousReceiver.Close()
	screen.tvdata = &soapcalls.TVPayload{ControlURL: previousReceiver.URL}
	ctx := t.Context()
	screen.torrent.ctx = ctx
	radioWindow(ctx, screen)
	r := screen.radio
	station := radiobrowser.Station{UUID: "next", Name: "Next", URL: "https://station.test/new"}
	r.client = radioTestDirectory{stations: []radiobrowser.Station{station}}
	test.Tap(card.radio)
	defer func() {
		if r.modal != nil {
			r.modal.Hide()
		}
	}()
	waitRadioUI(t, func() bool { return r.list.Length() == 1 })
	fyne.DoAndWait(func() { r.list.Select(0); test.Tap(r.useStation) })
	waitRadioUI(t, func() bool { return screen.MediaText.Text == station.StreamURL() && r.modal == nil })
	select {
	case url := <-stopped:
		if url != previousURL {
			t.Fatalf("old playback stopped after URL replacement: %q", url)
		}
	default:
		t.Fatal("old receiver did not receive Stop")
	}
	fyne.DoAndWait(func() {
		if screen.getScreenState() != "Stopped" || screen.tvdata != nil || r.selected != -1 || !r.useStation.Disabled() {
			t.Fatal("selection left playback/row active")
		}
	})
}

func TestRadioCloseClearsSelectedRowImmediately(t *testing.T) {
	screen, card := newMediaCardTestScreen(t)
	screen.Current = fyne.CurrentApp().NewWindow("Radio")
	defer screen.Current.Close()
	screen.Current.SetContent(card.content)
	radioWindow(context.Background(), screen)
	r := screen.radio
	r.client = radioTestDirectory{stations: []radiobrowser.Station{{UUID: "one", Name: "One", URL: "https://station.test/live"}}}
	test.Tap(card.radio)
	waitRadioUI(t, func() bool { return r.list.Length() == 1 })
	fyne.DoAndWait(func() {
		r.list.Select(0)
		r.modal.Hide()
		if r.selected != -1 || !r.useStation.Disabled() || !r.favorite.Disabled() {
			t.Fatal("Close retained selected station")
		}
	})
}

func TestRadioPopularReturnsFromFavoritesWithCurrentCountry(t *testing.T) {
	screen, _ := newMediaCardTestScreen(t)
	screen.Current = fyne.CurrentApp().NewWindow("Radio")
	defer screen.Current.Close()
	content := radioWindow(context.Background(), screen)
	screen.Current.SetContent(content)
	screen.Current.Resize(fyne.NewSize(620, 560))
	r := screen.radio
	popular := radiobrowser.Station{UUID: "popular", Name: "Popular station", URL: "https://station.test/popular"}
	saved := radiobrowser.Station{UUID: "saved", Name: "Saved station", URL: "https://station.test/saved"}
	queries := make(chan radioSearchRequest, 1)
	r.client = radioSearchRecordingDirectory{radioTestDirectory: radioTestDirectory{stations: []radiobrowser.Station{popular}}, queries: queries}
	r.favorites = []radiobrowser.Station{saved}
	r.countryCode = "GR"
	r.country.SetText("Greece")
	r.name.SetText("jazz")
	fyne.DoAndWait(func() {
		test.Tap(r.favoritesButton)
		if r.mode != radioModeFavorites || len(r.stations) != 1 || r.stations[0].UUID != saved.UUID || r.status.Text != fmt.Sprintf(lang.L("Saved %d favorites"), 1) {
			t.Fatal("Favorites did not show the saved station")
		}
		r.list.Select(0)
		test.Tap(r.popular)
	})
	waitRadioUI(t, func() bool { return len(r.stations) == 1 && r.stations[0].UUID == popular.UUID })
	select {
	case query := <-queries:
		if query.text != "" || query.country != "GR" {
			t.Fatalf("Popular query = %+v; expected no search and current country", query)
		}
	default:
		t.Fatal("Popular did not request directory stations")
	}
	fyne.DoAndWait(func() {
		if r.mode == radioModeFavorites || r.name.Text != "" || r.countryCode != "GR" || r.selected != -1 || !r.useStation.Disabled() {
			t.Fatal("Popular retained favorite/search selection or lost country")
		}
	})
}

func TestRadioReopensFavoritesWithoutReplacingThemWithSearchResults(t *testing.T) {
	screen, card := newMediaCardTestScreen(t)
	screen.Current = fyne.CurrentApp().NewWindow("Radio")
	defer screen.Current.Close()
	screen.Current.SetContent(card.content)
	radioWindow(context.Background(), screen)
	r := screen.radio
	saved := radiobrowser.Station{UUID: "saved", Name: "Saved", URL: "https://station.test/saved"}
	queries := make(chan radioSearchRequest, 2)
	r.client = radioSearchRecordingDirectory{radioTestDirectory: radioTestDirectory{stations: []radiobrowser.Station{{UUID: "popular", Name: "Popular", URL: "https://station.test/popular"}}}, queries: queries}
	r.favorites = []radiobrowser.Station{saved}
	fyne.DoAndWait(func() {
		test.Tap(card.radio)
		test.Tap(r.favoritesButton)
		r.modal.Hide()
		test.Tap(card.radio)
	})
	defer func() {
		fyne.DoAndWait(func() {
			if r.modal != nil {
				r.modal.Hide()
			}
		})
	}()
	waitRadioUI(t, func() bool { return len(queries) == 1 })
	fyne.DoAndWait(func() {
		if r.mode != radioModeFavorites || len(r.stations) != 1 || r.stations[0].UUID != saved.UUID {
			t.Fatal("reopening Favorites replaced saved stations with directory results")
		}
		r.list.Select(0)
		test.Tap(r.favorite)
		if len(r.stations) != 0 || len(r.favorites) != 0 {
			t.Fatal("reopened favorite could not be removed")
		}
	})
}

type radioSearchRequest struct {
	text, country string
}

type radioSearchRecordingDirectory struct {
	radioTestDirectory
	queries chan radioSearchRequest
}

func (d radioSearchRecordingDirectory) SearchText(ctx context.Context, text, country string) ([]radiobrowser.Station, error) {
	d.queries <- radioSearchRequest{text: text, country: country}
	return d.radioTestDirectory.SearchText(ctx, text, country)
}

func TestRadioArtworkReturnsAfterTemporaryDesktopModes(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	asset, err := metadata.LoadArtwork(radioTestPNG(t), "logo")
	if err != nil {
		t.Fatal(err)
	}
	station := radiobrowser.Station{UUID: "one", Name: "Station", URL: "https://station.test/live"}
	tt := []struct {
		name  string
		enter func(*FyneScreen)
		exit  func(*FyneScreen)
	}{
		{name: "Cast Desktop", enter: func(s *FyneScreen) { s.Screencast = true }, exit: func(s *FyneScreen) { s.Screencast = false }},
		{name: "RTMP", enter: func(s *FyneScreen) { s.rtmpServerCheck.SetChecked(true) }, exit: func(s *FyneScreen) { s.rtmpServerCheck.SetChecked(false) }},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			screen := &FyneScreen{radioSelected: station, currentArtworkIdentity: radioArtworkIdentity(station), selectedArtwork: newSelectedArtwork(), rtmpServerCheck: widget.NewCheck("", nil)}
			screen.setSelectedRadioArtwork(station, asset)
			tc.enter(screen)
			screen.invalidateRadioURL("temporary streaming mode")
			screen.previewRadioArtwork(nil)
			screen.setCurrentArtwork(nil)
			// A favicon may also complete while the temporary mode is active.
			screen.setSelectedRadioArtwork(station, asset)
			tc.exit(screen)
			screen.invalidateRadioURL(station.StreamURL())
			if screen.radioTitle(station.StreamURL()) != station.Name {
				t.Fatal("temporary mode erased radio identity")
			}
			if screen.getCurrentArtwork() != asset || !screen.selectedArtwork.picture.Visible() {
				t.Fatal("temporary mode permanently erased radio artwork")
			}
			screen.invalidateRadioURL("https://other.test/live")
			if screen.radioSelected.UUID != "" || screen.getCurrentArtwork() != nil {
				t.Fatal("manual URL edit failed to invalidate radio")
			}
		})
	}
}

func TestURLInputSurvivesLocalFileRoundTrip(t *testing.T) {
	asset, err := metadata.LoadArtwork(radioTestPNG(t), "logo")
	if err != nil {
		t.Fatal(err)
	}
	station := radiobrowser.Station{UUID: "one", Name: "Station", URL: "https://station.test/live", Favicon: "https://station.test/logo.png"}
	tt := []struct {
		name  string
		radio bool
		url   string
	}{
		{name: "radio station", radio: true, url: station.StreamURL()},
		{name: "typed URL", url: "https://example.org/movie.mp4"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			screen, card := newMediaCardTestScreen(t)
			screen.selectedArtwork = newSelectedArtwork()
			screen.TranscodeCheckBox = widget.NewCheck("", nil)
			screen.MediaText.OnChanged = screen.invalidateRadioURL
			fyne.DoAndWait(func() {
				if tc.radio {
					screen.selectRadioStation(station)
					screen.setSelectedRadioArtwork(station, asset)
				} else {
					card.source.SetSelected(lang.L("URL"))
					screen.MediaText.SetText(tc.url)
				}
				card.source.SetSelected(lang.L("Local File"))
				card.source.SetSelected(lang.L("URL"))
				if screen.MediaText.Text != tc.url {
					t.Fatalf("URL input = %q, want %q", screen.MediaText.Text, tc.url)
				}
				if !tc.radio {
					return
				}
				if screen.radioTitle(tc.url) != station.Name {
					t.Fatal("returning to URL lost the radio station")
				}
				if !screen.selectedArtwork.picture.Visible() {
					t.Fatal("returning to URL lost the station artwork")
				}
			})
		})
	}
}

func TestURLSubtitleAvailabilityFollowsInput(t *testing.T) {
	tt := []struct {
		name         string
		stationURL   string
		typed        string
		wantDisabled bool
	}{
		{name: "radio without extension", stationURL: "https://station.test/live", wantDisabled: true},
		{name: "video URL replaces radio", stationURL: "https://station.test/live.mp3", typed: "https://example.org/movie.mp4"},
		{name: "audio URL with query", typed: "https://example.org/song.mp3?token=1", wantDisabled: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			screen, card := newMediaCardTestScreen(t)
			screen.selectedArtwork = newSelectedArtwork()
			screen.TranscodeCheckBox = widget.NewCheck("", nil)
			screen.MediaText.OnChanged = func(v string) {
				screen.invalidateRadioURL(v)
				card.refresh()
			}
			fyne.DoAndWait(func() {
				if tc.stationURL != "" {
					screen.selectRadioStation(radiobrowser.Station{UUID: "one", Name: "Station", URL: tc.stationURL, Codec: "MP3"})
				} else {
					card.source.SetSelected(lang.L("URL"))
				}
				if tc.typed != "" {
					screen.MediaText.SetText(tc.typed)
				}
				if got := card.subtitles.Disabled(); got != tc.wantDisabled {
					t.Fatalf("subtitles disabled = %v, want %v", got, tc.wantDisabled)
				}
			})
		})
	}
}
