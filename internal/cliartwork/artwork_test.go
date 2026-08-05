package cliartwork

import (
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go2tv.app/go2tv/v2/httphandlers"
)

func TestPrepareRegistersLocalAudioArtwork(t *testing.T) {
	directory := t.TempDir()
	mediaPath := filepath.Join(directory, "track.mp3")
	if err := os.WriteFile(mediaPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeJPEG(t, filepath.Join(directory, "track.jpg"), 800, 400)

	server := httphandlers.NewServer("127.0.0.1:1234")
	artwork := Prepare(server, mediaPath, server.GetAddr(), true)
	if artwork == nil {
		t.Fatal("expected artwork")
	}
	if artwork.MIMEType != "image/jpeg" || artwork.Width != 600 || artwork.Height != 300 {
		t.Fatalf("unexpected artwork: %+v", artwork)
	}
	if !strings.HasPrefix(artwork.URL, "http://127.0.0.1:1234/artwork/") || !strings.HasSuffix(artwork.URL, ".jpg") {
		t.Fatalf("unexpected artwork URL: %q", artwork.URL)
	}

	artworkURL, err := url.Parse(artwork.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, artworkURL.Path, nil)
	response := httptest.NewRecorder()
	server.ServeMediaHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status: got %d want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type: got %q want image/jpeg", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("CORS: got %q want *", got)
	}
}

func TestPrepareIgnoresNonLocalOrUnusableArtwork(t *testing.T) {
	directory := t.TempDir()
	mediaPath := filepath.Join(directory, "track.mp3")
	if err := os.WriteFile(mediaPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeJPEG(t, filepath.Join(directory, "track.jpg"), 20, 20)

	tests := []struct {
		name  string
		path  string
		local bool
	}{
		{name: "remote", path: "https://example.com/track.mp3", local: false},
		{name: "stdin", path: "stdin.stream", local: false},
		{name: "no artwork", path: filepath.Join(directory, "missing.mp3"), local: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httphandlers.NewServer("127.0.0.1:1234")
			if artwork := Prepare(server, test.path, server.GetAddr(), test.local); artwork != nil {
				t.Fatalf("unexpected artwork: %+v", artwork)
			}
		})
	}
}

func TestPrepareIgnoresInvalidArtwork(t *testing.T) {
	directory := t.TempDir()
	mediaPath := filepath.Join(directory, "track.mp3")
	if err := os.WriteFile(mediaPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "track.jpg"), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := httphandlers.NewServer("127.0.0.1:1234")
	if artwork := Prepare(server, mediaPath, server.GetAddr(), true); artwork != nil {
		t.Fatalf("unexpected artwork: %+v", artwork)
	}
}

func writeJPEG(t *testing.T, path string, width, height int) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 120, A: 255})
		}
	}
	if err := jpeg.Encode(file, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
}
