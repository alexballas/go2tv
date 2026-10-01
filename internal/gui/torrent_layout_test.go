package gui

import (
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
)

func TestTorrentFormKeepsWrappedStatusAboveSelection(t *testing.T) {
	tt := []struct {
		name  string
		width float32
	}{
		{"portrait phone", 280},
		{"wide phone", 360},
		{"desktop", 480},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			app := test.NewApp()
			t.Cleanup(app.Quit)
			status := widget.NewLabel("Fetching torrent metadata…")
			status.Wrapping = fyne.TextWrapWord
			files := widget.NewSelect([]string{"Sintel.mp4 (123.3 MiB)"}, nil)
			choose := widget.NewButton("Use file", nil)
			content := newTorrentDialogContent(status, files, choose)
			content.Resize(fyne.NewSize(tc.width, 250))
			for _, text := range []string{
				"Choose a file, then cast. Missing pieces buffer automatically.",
				"Could not fetch torrent metadata. Check the connection and try another magnet link or torrent file.",
				"Enter a magnet link.",
			} {
				status.SetText(text)
				content.Content.Refresh()
				if status.Size().Height < status.MinSize().Height {
					t.Fatalf("status clipped: allocated %v, required %v", status.Size(), status.MinSize())
				}
				if files.Position().Y < status.Position().Y+status.Size().Height+theme.Padding() {
					t.Fatal("file selector overlaps wrapped status")
				}
				gap := choose.Position().Y - files.Position().Y - files.Size().Height
				if gap != theme.Padding() {
					t.Fatalf("Use file separated from selector by %v, want %v", gap, theme.Padding())
				}
			}
		})
	}
}
