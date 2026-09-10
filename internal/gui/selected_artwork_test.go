//go:build !(android || ios)

package gui

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/mediaartwork"
	"go2tv.app/go2tv/v2/internal/mediamodel"
)

func TestSelectedArtworkLoadsWithoutPlayback(t *testing.T) {
	dir := t.TempDir()
	track := filepath.Join(dir, "track.mp3")
	if err := os.WriteFile(track, []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	cover := filepath.Join(dir, "track.png")
	writeQueueThumbnailArtwork(t, cover)
	tt := []struct {
		name, path string
		kind       mediamodel.MediaKind
		want       bool
	}{
		{"audio sidecar", track, mediamodel.MediaKindAudio, true},
		{"selected image", cover, mediamodel.MediaKindImage, true},
		{"unavailable file", filepath.Join(dir, "missing.png"), mediamodel.MediaKindImage, false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got := loadSelectedArtwork(context.Background(), mediaartwork.Request{Path: tc.path, Kind: tc.kind})
			if (got != nil) != tc.want {
				t.Fatalf("artwork available = %v, want %v", got != nil, tc.want)
			}
		})
	}
}

func TestSelectedArtworkClearRejectsLateResult(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	card := newSelectedArtwork()
	old, cancel := context.WithCancel(context.Background())
	card.cancel = cancel
	first := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	card.applyArtwork(old, first)
	if !card.picture.Visible() || card.placeholder.Visible() {
		t.Fatal("loaded artwork not displayed")
	}
	screen := &FyneScreen{selectedArtwork: card, SelectInternalSubs: widget.NewSelect(nil, nil), PlayPause: widget.NewButton("", nil)}
	clearCurrentMediaSelection(screen)
	card.applyArtwork(old, first)
	if card.picture.Visible() || card.picture.Image != nil || !card.placeholder.Visible() {
		t.Fatal("late result restored cleared artwork")
	}
	next := image.NewNRGBA(image.Rect(0, 0, 30, 30))
	card.applyArtwork(context.Background(), next)
	card.applyArtwork(old, first)
	if card.picture.Image != next {
		t.Fatal("old result replaced new selection")
	}
}

func TestArtworkPlaybackRowKeepsSquareAndSpace(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	art := newSelectedArtwork()
	modes := container.NewGridWithColumns(3,
		newPlaybackToggle("Loop", playbackLoopIcon()),
		newPlaybackToggle("Auto-play", playbackAutoplayIcon()),
		newPlaybackToggle("Transcode", playbackTranscodeIcon()),
	)
	playback := container.NewVBox(widget.NewLabel("Ready to cast"), widget.NewSlider(0, 100), widget.NewButton("Cast", nil))
	row := container.New(artworkPlaybackLayout{}, art, playback, modes)
	for _, width := range []float32{row.MinSize().Width, 640, 900} {
		row.Resize(fyne.NewSize(width, row.MinSize().Height))
		if art.Size().Height != playback.Size().Height {
			t.Fatal("artwork bottom must align with the transport row")
		}
		if modes.Position().X != playback.Position().X || modes.Position().Y < art.Size().Height {
			t.Fatal("modes must align with controls below the artwork")
		}
		if art.Size().Width != art.Size().Height {
			t.Fatal("artwork is not square")
		}
		if playback.Size().Height < playback.MinSize().Height || playback.Position().Y != art.Position().Y {
			t.Fatal("controls must fit and align with the thumbnail top")
		}
		if playback.Position().X < art.Size().Width || playback.Size().Width < playback.MinSize().Width {
			t.Fatal("artwork crowds playback controls")
		}
	}
}
