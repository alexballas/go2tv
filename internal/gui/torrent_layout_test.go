package gui

import (
	"strings"
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
)

func TestTorrentDialogFitsForm(t *testing.T) {
	tt := []struct {
		name   string
		size   fyne.Size
		status string
		scroll bool
	}{
		{name: "desktop", size: fyne.NewSize(900, 700)},
		{name: "small desktop", size: fyne.NewSize(523, 330)},
		{name: "portrait phone", size: fyne.NewSize(360, 760)},
		{name: "short window", size: fyne.NewSize(523, 250), scroll: true},
		{name: "long error", size: fyne.NewSize(360, 500), status: strings.Repeat("Could not fetch torrent metadata. Check the connection and try another file. ", 8), scroll: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			app := test.NewApp()
			t.Cleanup(app.Quit)
			s := &FyneScreen{Current: app.NewWindow("Torrent")}
			s.Current.Resize(tc.size)
			entry := widget.NewEntry()
			entry.SetPlaceHolder("Paste magnet link")
			entry.SetText("magnet:?xt=urn:btih:0123456789012345678901234567890123456789&dn=Tears%20of%20Steel.mp4")
			status := widget.NewLabel("Choose a file, then cast. Missing pieces buffer automatically.")
			if tc.status != "" {
				status.SetText(tc.status)
			}
			status.Wrapping = fyne.TextWrapWord
			files := widget.NewSelect([]string{"Tears of Steel.mp4 (482.1 MiB)"}, nil)
			files.SetSelectedIndex(0)
			choose := widget.NewButton("Use file", nil)
			content := newTorrentDialogContent(entry,
				container.NewGridWithColumns(2, widget.NewButton("Load magnet", nil), widget.NewButton("Open .torrent", nil)),
				status, files, choose,
			)
			d := dialog.NewCustom("Torrent", "Cancel", content, s.Current)
			resizeTorrentDialog(s, d, content)
			d.Show()
			t.Cleanup(d.Hide)
			if content.Content.Size().Width > content.Size().Width {
				t.Fatalf("form overflows horizontally: content %v, viewport %v", content.Content.Size(), content.Size())
			}
			if status.Size().Height < status.MinSize().Height || files.Position().Y < status.Position().Y+status.Size().Height {
				t.Fatal("wrapped status clips or overlaps the file selector")
			}
			if tc.scroll {
				if content.Content.MinSize().Height <= content.Size().Height {
					t.Fatal("oversized form does not scroll")
				}
				content.ScrollToBottom()
			}
			bottom := choose.Position().Y + choose.Size().Height - content.Offset.Y
			if bottom > content.Size().Height {
				t.Fatalf("Use file clipped: bottom %v, viewport height %v", bottom, content.Size().Height)
			}
		})
	}
}

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
