package gui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/theme"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/metadata"
)

func radioTestPNG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 24, 16))); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func radioTestGIF(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := gif.Encode(&data, image.NewPaletted(image.Rect(0, 0, 24, 16), color.Palette{color.Black, color.White}), nil); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestRadioArtworkFollowsStationAndReceiverRoute(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	asset, err := metadata.LoadArtwork(radioTestPNG(t), "radio icon")
	if err != nil {
		t.Fatal(err)
	}
	old := radiobrowser.Station{UUID: "one", URL: "https://station.test/old"}
	selected := radiobrowser.Station{UUID: "one", URL: "https://station.test/new"}
	screen := &FyneScreen{radioSelected: selected, currentArtworkIdentity: radioArtworkIdentity(selected)}
	screen.setSelectedRadioArtwork(old, asset)
	if screen.getCurrentArtwork() != nil {
		t.Fatal("stale icon replaced selected station")
	}
	screen.setSelectedRadioArtwork(selected, asset)
	if got := screen.resolveCurrentRadioArtwork(context.Background(), selected.StreamURL()); got != asset {
		t.Fatal("radio artwork lost at cast startup")
	}
	server := httphandlers.NewServer("")
	registerGUIArtwork(server, asset)
	response := httptest.NewRecorder()
	server.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, asset.HandlerPath(), nil))
	if response.Code != 200 || response.Header().Get("Content-Type") != "image/jpeg" || !bytes.Equal(response.Body.Bytes(), asset.Data) {
		t.Fatal("receiver logo route does not serve normalized artwork")
	}
	address := chromecastRadioArtworkAddress("station.test", "192.0.2.1:3500", asset)
	media := guiMediaMetadata("Radio", address, asset)
	if media.Artwork == nil || media.Artwork.URL != "http://192.0.2.1:3500"+asset.HandlerPath() {
		t.Fatalf("direct cast logo address = %+v", media.Artwork)
	}
	screen.invalidateRadioURL("https://unrelated.test/live")
	screen.setSelectedRadioArtwork(selected, asset)
	if screen.getCurrentArtwork() != nil || screen.radioTitle(selected.StreamURL()) != "" {
		t.Fatal("late favicon overwrote pasted URL")
	}
}

func TestRadioListUsesStationLogoAndBoundsCache(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	asset, err := metadata.LoadArtwork(radioTestPNG(t), "logo")
	if err != nil {
		t.Fatal(err)
	}
	screen := &FyneScreen{Current: app.NewWindow("Radio")}
	radioWindow(context.Background(), screen)
	r := screen.radio
	station := radiobrowser.Station{UUID: "station", Name: "Station", Country: "Greece", Codec: "AAC", Favicon: "https://station.test/logo.png", URL: "https://station.test/live"}
	r.artwork.mu.Lock()
	for i := range maxRadioIconCacheEntries + 10 {
		r.artwork.storeLocked(fmt.Sprint(i), asset, nil)
	}
	entries := len(r.artwork.entries)
	r.artwork.storeLocked(station.Icon().Key(), asset, nil)
	r.artwork.mu.Unlock()
	if entries != maxRadioIconCacheEntries {
		t.Fatalf("icon cache grew to %d entries", entries)
	}
	r.setStations([]radiobrowser.Station{station})
	row := r.list.CreateItem()
	r.favorites = []radiobrowser.Station{station}
	r.list.UpdateItem(0, row)
	stationRow := row.(*radioStationRow)
	if !bytes.Equal(stationRow.icon.Resource.Content(), asset.Data) || stationRow.title.Text != "★ Station" || stationRow.details.Text != "Greece · AAC" {
		t.Fatal("station row lacks fetched logo")
	}
	// Virtualized rows must clear the previous station's favorite marker,
	// metadata and logo when reused for a station without artwork.
	r.setStations([]radiobrowser.Station{{UUID: "other", Name: "Other", Country: "France", Codec: "MP3"}})
	r.list.UpdateItem(0, row)
	if stationRow.title.Text != "Other" || stationRow.details.Text != "France · MP3" || stationRow.icon.Resource.Name() != theme.MediaMusicIcon().Name() {
		t.Fatal("recycled row retained previous station metadata or artwork")
	}
}
