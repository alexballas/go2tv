//go:build android || ios

package gui

import (
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/storage"

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
		s.subsfile = nil
		s.setCurrentArtwork(nil)
		s.MediaText.SetText(fileSourceName(path))
		setPlayPauseView("", s)
	})
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
