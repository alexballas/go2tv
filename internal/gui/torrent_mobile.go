//go:build android || ios

package gui

import (
	"io"
	"path/filepath"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/utils"
)

func torrentCacheDir() (string, error) { return mobileCacheDir() }

func newTorrentButton(s *FyneScreen) *widget.Button {
	return widget.NewButtonWithIcon(lang.L("Torrent…"), theme.DownloadIcon(), func() { showTorrentDialog(s) })
}

func showTorrentDocument(s *FyneScreen, reader io.ReadCloser) {
	showTorrentInputDialog(s, reader)
}

func selectTorrentMedia(s *FyneScreen, path string) {
	fyne.DoAndWait(func() {
		if s.ExternalMediaURL != nil {
			s.ExternalMediaURL.SetChecked(false)
		}
		s.mediafile = storage.NewFileURI(path)
		clearsubsAction(s)
		s.setCurrentArtwork(nil)
		s.MediaText.SetText(filepath.Base(path))
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
