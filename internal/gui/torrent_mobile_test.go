//go:build android || ios

package gui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/mediasource"
)

type unavailableMobileTorrentSource struct{ mediasource.Source }

func (unavailableMobileTorrentSource) MIME() string { return "video/mp4" }
func (unavailableMobileTorrentSource) Open(context.Context) (io.ReadSeekCloser, error) {
	return nil, os.ErrNotExist
}

func newMobileTorrentTestScreen(t *testing.T) *FyneScreen {
	t.Helper()
	app := test.NewApp()
	t.Cleanup(app.Quit)
	return &FyneScreen{
		Current:   app.NewWindow("Torrent"),
		MediaText: widget.NewEntry(), SubsText: widget.NewEntry(),
		PlayPause:        widget.NewButton("Play", nil),
		ExternalMediaURL: widget.NewCheck("", nil),
		CurrentPos:       binding.NewString(), EndPos: binding.NewString(),
		SlideBar: &tappedSlider{},
	}
}

func TestMobileTorrentUsesProgressiveSource(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	path := filepath.Join(t.TempDir(), "movie.mp4")
	t.Cleanup(mediasource.Register(path, unavailableMobileTorrentSource{}))
	s.mediafile = storage.NewFileURI(path)
	// No backing file or available pieces: selection and MIME must not read it.
	mime, err := mobileMediaMIME(s.mediafile)
	if err != nil || mime != "video/mp4" {
		t.Fatalf("MIME = %q, err = %v", mime, err)
	}
	media, err := seekableMediaForCasting(s)
	if err != nil || media != path || s.tempMediaFile != "" {
		t.Fatalf("media = %v, err = %v, temp = %q", media, err, s.tempMediaFile)
	}
	media, err = mobileMediaForTranscodedSeek(s)
	if err != nil || media != path {
		t.Fatalf("seek media = %v, err = %v", media, err)
	}
}

func TestMobileTorrentSelectionClearsSubtitles(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	s.subsfile = storage.NewFileURI("/old.srt")
	s.SubsText.SetText("old.srt")
	path := filepath.Join(t.TempDir(), "subs.srt")
	if err := os.WriteFile(path, []byte("old subtitles"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.tempSubsFile = path
	mediaPath := filepath.Join(t.TempDir(), "movie.mp4")
	selectTorrentMedia(s, mediaPath)
	if s.subsfile != nil || s.SubsText.Text != "" || s.tempSubsFile != "" {
		t.Fatal("torrent selection retained previous subtitles")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary subtitles retained: %v", err)
	}
	if s.mediafile.Path() != mediaPath || s.MediaText.Text != "movie.mp4" {
		t.Fatal("torrent media selection missing")
	}
}

func TestMobileClearTorrentSelection(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	path := filepath.Join(t.TempDir(), "movie.mp4")
	t.Cleanup(mediasource.Register(path, unavailableMobileTorrentSource{}))
	s.mediafile = storage.NewFileURI(path)
	s.MediaText.SetText("movie.mp4")
	clearmediaAction(s)
	if s.mediafile != nil || s.MediaText.Text != "" {
		t.Fatal("clear retained the torrent URI while cancellation ran")
	}
	s.mediafile = storage.NewFileURI("/replacement.mp4")
	s.MediaText.SetText("replacement.mp4")
	clearTorrentSelection(s, path)
	if s.MediaText.Text != "replacement.mp4" {
		t.Fatal("torrent cleanup cleared replacement media")
	}
}

func TestMobileTorrentDialogFitsContent(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	s.Current.Resize(fyne.NewSize(360, 760))
	status := widget.NewLabel("Choose a file, then cast. Missing pieces buffer automatically.")
	status.Wrapping = fyne.TextWrapWord
	files := widget.NewSelect([]string{"Sintel.mp4 (123.3 MiB)"}, nil)
	files.SetSelectedIndex(0)
	choose := widget.NewButton("Use file", nil)
	content := newTorrentDialogContent(
		widget.NewEntry(),
		container.NewGridWithColumns(2, widget.NewButton("Load magnet", nil), widget.NewButton("Open .torrent", nil)),
		status, files, choose,
	)
	d := dialog.NewCustom("Torrent", "Cancel", content, s.Current)
	resizeTorrentDialog(s, d, content)
	d.Show()
	t.Cleanup(d.Hide)
	if content.Size().Height > s.Current.Canvas().Size().Height/2 {
		t.Fatalf("short torrent form expanded to %v in %v canvas", content.Size(), s.Current.Canvas().Size())
	}
	if content.Size().Height < content.Content.MinSize().Height {
		t.Fatal("short torrent form requires scrolling")
	}
	if files.Position().Y < status.Position().Y+status.Size().Height {
		t.Fatal("file selector overlaps wrapped status")
	}
}
