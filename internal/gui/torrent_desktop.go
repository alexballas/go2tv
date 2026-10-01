//go:build !(android || ios)

package gui

import (
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/dialog"

	"go2tv.app/go2tv/v2/internal/mediasource"
)

func torrentCacheDir() (string, error) { return "", nil }

func resizeTorrentDialog(_ *FyneScreen, d dialog.Dialog, _ *container.Scroll) {
	d.Resize(fyne.NewSize(520, 300))
}

func syncTorrentBackgroundSession(_ *FyneScreen) {}

func mediaPreviewInput(path string) string { return mediasource.Input(path) }

func selectTorrentMedia(s *FyneScreen, path string) {
	s.replaceSessionQueue(nil, -1)
	if s.ExternalMediaURL != nil && s.ExternalMediaURL.Checked {
		fyne.DoAndWait(func() { s.ExternalMediaURL.SetChecked(false) })
	}
	s.mediafile = path
	s.subsfile = ""
	s.setCurrentArtwork(nil)
	fyne.Do(func() {
		if s.NextMediaCheck != nil {
			s.NextMediaCheck.SetChecked(false)
		}
		s.MediaText.SetText(fileSourceName(path))
		setInternalSubsDropdownNoSubs(s)
		s.refreshQueueStateUI()
		setPlayPauseView("", s)
	})
}

func torrentMediaSelected(s *FyneScreen) bool {
	_, ok := mediasource.Lookup(s.mediafile)
	return ok
}

func clearTorrentSelection(s *FyneScreen, path string) {
	if s.mediafile == path {
		clearCurrentMediaSelection(s)
	}
}
