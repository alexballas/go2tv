//go:build android || ios

package gui

import (
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/theme"

	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/utils"
)

func torrentCacheDir() (string, error) { return mobileCacheDir() }

func selectTorrentMedia(s *FyneScreen, path string) {
	fyne.DoAndWait(func() {
		if s.ExternalMediaURL != nil {
			s.ExternalMediaURL.SetChecked(false)
		}
		s.mediafile = storage.NewFileURI(path)
		clearsubsAction(s)
		s.setCurrentArtwork(nil)
		s.MediaText.SetText(fileSourceName(path))
		setPlayPauseView("", s)
		syncBackgroundSession(s, s.getScreenState())
	})
}

func openMobileTorrentDocument(s *FyneScreen, uri fyne.URI) {
	go func() {
		reader, err := storage.Reader(uri)
		fyne.Do(func() {
			if err != nil {
				dialog.ShowError(err, s.Current)
				return
			}
			showTorrentDocument(s, reader)
		})
	}()
}

func resizeTorrentDialog(s *FyneScreen, d dialog.Dialog, content *container.Scroll) {
	size := s.Current.Canvas().Size()
	if canvas, ok := s.Current.Canvas().(interface {
		PopUpArea() (fyne.Position, fyne.Size)
	}); ok {
		_, size = canvas.PopUpArea()
	}
	width := min(float32(520), size.Width-2*theme.Padding())
	// Dialog chrome adds 32 points of horizontal padding, plus popup padding.
	content.Content.Resize(fyne.NewSize(width-32-2*theme.Padding(), content.Content.MinSize().Height))
	chromeHeight := d.MinSize().Height - content.MinSize().Height
	height := min(content.Content.MinSize().Height, max(float32(32), size.Height*0.8-chromeHeight))
	content.SetMinSize(fyne.NewSize(0, height))
	d.Resize(fyne.NewSize(width, height+chromeHeight))
}

func syncTorrentBackgroundSession(s *FyneScreen) {
	syncBackgroundSession(s, s.getScreenState())
}

func torrentMediaSelected(s *FyneScreen) bool {
	if s.mediafile == nil {
		return false
	}
	_, ok := mediasource.Lookup(s.mediafile.Path())
	return ok
}

func clearTorrentSelection(s *FyneScreen, path string) {
	if s.mediafile != nil && s.mediafile.Path() == path {
		clearmediaAction(s)
	}
}

func mobileMediaMIME(uri fyne.URI) (string, error) {
	if source, ok := mediasource.Lookup(uri.Path()); ok {
		return source.MIME(), nil
	}
	reader, err := storage.Reader(uri)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	return utils.GetMimeDetailsFromStream(reader)
}
