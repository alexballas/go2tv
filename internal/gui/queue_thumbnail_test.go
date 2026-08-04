//go:build !(android || ios)

package gui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexballas/refyne/v2/test"
	"go2tv.app/go2tv/v2/internal/mediamodel"
)

func TestQueueAudioArtworkThumbnail(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	dir := t.TempDir()
	trackPath := filepath.Join(dir, "track.mp3")
	if err := os.WriteFile(trackPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeQueueThumbnailArtwork(t, filepath.Join(dir, "track.png"))

	screen := &FyneScreen{}
	thumbnail := screen.queueMediaThumbnail(trackPath, mediamodel.MediaKindAudio)
	if thumbnail == nil || thumbnail.Image == nil {
		t.Fatal("audio artwork thumbnail not loaded")
	}
	if !queueItemNeedsThumbnail("audio") {
		t.Fatal("audio queue item does not request thumbnail")
	}
}

func TestQueueAudioThumbnailMissingArtwork(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	trackPath := filepath.Join(t.TempDir(), "plain.mp3")
	if err := os.WriteFile(trackPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if thumbnail := (&FyneScreen{}).queueMediaThumbnail(trackPath, mediamodel.MediaKindAudio); thumbnail != nil {
		t.Fatal("unexpected thumbnail without artwork")
	}
}

func writeQueueThumbnailArtwork(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	artwork := image.NewNRGBA(image.Rect(0, 0, 48, 24))
	for y := range 24 {
		for x := range 48 {
			artwork.SetNRGBA(x, y, color.NRGBA{R: 180, G: 80, A: 255})
		}
	}
	if err := png.Encode(file, artwork); err != nil {
		t.Fatal(err)
	}
}
