//go:build !(android || ios)

package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go2tv.app/go2tv/v2/httphandlers"
)

func TestResolveGUIArtworkReplacesStaleState(t *testing.T) {
	dir := t.TempDir()
	trackPath := filepath.Join(dir, "track.mp3")
	noArtworkPath := filepath.Join(dir, "plain.mp3")
	for _, mediaPath := range []string{trackPath, noArtworkPath} {
		if err := os.WriteFile(mediaPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cover := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := range 20 {
		for x := range 40 {
			cover.Set(x, y, color.RGBA{R: 120, G: 20, B: 60, A: 255})
		}
	}
	coverFile, err := os.Create(filepath.Join(dir, "track.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(coverFile, cover); err != nil {
		coverFile.Close()
		t.Fatal(err)
	}
	if err := coverFile.Close(); err != nil {
		t.Fatal(err)
	}

	screen := &FyneScreen{}
	asset := resolveGUIArtwork(trackPath, "audio/mpeg", true)
	if asset == nil {
		t.Fatal("local audio artwork = nil")
	}
	screen.setCurrentArtwork(asset)

	tests := []struct {
		name      string
		mediaPath string
		mediaType string
		local     bool
	}{
		{name: "no artwork", mediaPath: noArtworkPath, mediaType: "audio/mpeg", local: true},
		{name: "external", mediaPath: trackPath, mediaType: "audio/mpeg", local: false},
		{name: "non audio", mediaPath: trackPath, mediaType: "video/mp4", local: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			screen.setCurrentArtwork(asset)
			screen.setCurrentArtwork(resolveGUIArtwork(tt.mediaPath, tt.mediaType, tt.local))
			if got := screen.getCurrentArtwork(); got != nil {
				t.Fatalf("current artwork = %q, want nil", got.Source)
			}
		})
	}
}

func TestRegisterGUIArtworkAndBuildMetadata(t *testing.T) {
	data := desktopArtworkJPEG(t)
	asset := resolveGUIArtwork(data.mediaPath, "audio/mpeg", true)
	if asset == nil {
		t.Fatal("artwork = nil")
	}

	for restart := range 2 {
		server := httphandlers.NewServer("127.0.0.1:0")
		registerGUIArtwork(server, asset)

		req := httptest.NewRequest(http.MethodGet, asset.HandlerPath(), nil)
		recorder := httptest.NewRecorder()
		server.ServeMediaHandler().ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("restart %d: status = %d, want %d", restart, recorder.Code, http.StatusOK)
		}
		if got := recorder.Header().Get("Content-Type"); got != "image/jpeg" {
			t.Fatalf("restart %d: Content-Type = %q", restart, got)
		}
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Fatalf("restart %d: CORS = %q", restart, got)
		}
		if !bytes.Equal(recorder.Body.Bytes(), asset.Data) {
			t.Fatalf("restart %d: served artwork differs", restart)
		}
	}

	mediaMetadata := guiMediaMetadata("track.mp3", "192.0.2.1:8080", asset)
	if mediaMetadata.Title != "track.mp3" || mediaMetadata.Artwork == nil {
		t.Fatalf("metadata = %+v", mediaMetadata)
	}
	wantURL := "http://192.0.2.1:8080" + asset.HandlerPath()
	if mediaMetadata.Artwork.URL != wantURL || mediaMetadata.Artwork.Width != 40 || mediaMetadata.Artwork.Height != 20 {
		t.Fatalf("artwork metadata = %+v, want URL %q dimensions 40x20", mediaMetadata.Artwork, wantURL)
	}
	if noArtwork := guiMediaMetadata("plain.mp3", "192.0.2.1:8080", nil); noArtwork.Artwork != nil {
		t.Fatalf("no-art metadata = %+v", noArtwork)
	}
}

type desktopArtworkFixture struct {
	mediaPath string
}

func desktopArtworkJPEG(t *testing.T) desktopArtworkFixture {
	t.Helper()
	dir := t.TempDir()
	mediaPath := filepath.Join(dir, "track.mp3")
	if err := os.WriteFile(mediaPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	cover := image.NewRGBA(image.Rect(0, 0, 40, 20))
	coverFile, err := os.Create(filepath.Join(dir, "track.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(coverFile, cover); err != nil {
		coverFile.Close()
		t.Fatal(err)
	}
	if err := coverFile.Close(); err != nil {
		t.Fatal(err)
	}
	return desktopArtworkFixture{mediaPath: mediaPath}
}
