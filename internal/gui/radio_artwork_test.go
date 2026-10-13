package gui

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRadioFaviconIsBoundedAndNormalized(t *testing.T) {
	valid := radioTestPNG(t)
	largePixels := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(largePixels[16:20], 4096)
	binary.BigEndian.PutUint32(largePixels[20:24], 4096)
	binary.BigEndian.PutUint32(largePixels[29:33], crc32.ChecksumIEEE(largePixels[12:29]))
	tt := []struct {
		name   string
		body   []byte
		status int
		want   bool
	}{
		{name: "PNG station logo", body: valid, status: 200, want: true},
		{name: "SVG fallback", body: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), status: 200},
		{name: "oversized body", body: []byte(strings.Repeat("x", maxRadioIconBytes+1)), status: 200},
		{name: "pixel limit", body: largePixels, status: 200},
		{name: "missing icon", status: 404},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write(tc.body) }))
			defer upstream.Close()
			asset, err := fetchRadioArtwork(context.Background(), upstream.URL)
			if (err == nil && asset != nil) != tc.want {
				t.Fatalf("icon result = %v, %v", asset, err)
			}
			if tc.want && (asset.MIMEType != "image/jpeg" || asset.Width != 24 || asset.Height != 16) {
				t.Fatalf("normalized icon = %+v", asset)
			}
		})
	}
	if _, err := fetchRadioArtwork(context.Background(), "file:///tmp/logo.png"); err == nil {
		t.Fatal("local file favicon accepted")
	}
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
	r.artwork.storeLocked(station.Favicon, asset, nil)
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
