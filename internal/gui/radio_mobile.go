//go:build android || ios

package gui

import (
	"github.com/alexballas/refyne/v2/lang"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/metadata"
)

func (s *FyneScreen) validateRadioSelection() error { return nil }

func (s *FyneScreen) cancelQueuedRadioPlayback() {
	// Share the registration lock so cancellation cannot miss the handoff.
	// Same-station selection preserves a startup already sent to the receiver.
	s.torrent.mu.Lock()
	defer s.torrent.mu.Unlock()
	if s.torrent.startup == nil {
		s.cancelMobilePlayback()
	}
}

// Mobile playback claims the UI thread with DoAndWait.
func (s *FyneScreen) playRadioStation() { go playAction(s) }

func (s *FyneScreen) radioSongDisplayAvailable() bool { return s.radioSong.label != nil }

func (s *FyneScreen) refreshRadioSongView() {
	s.mu.RLock()
	label, title := s.radioSong.label, s.radioSong.title
	s.mu.RUnlock()
	if label == nil {
		return
	}
	if title == "" {
		label.SetText("")
		label.Hide()
		return
	}
	label.SetText(lang.L("Now playing") + ": " + title)
	label.Show()
}

func (s *FyneScreen) selectRadioStation(station radiobrowser.Station) {
	transcode := s.TranscodeCheckBox.Checked
	s.ExternalMediaURL.SetChecked(true)
	clearsubsAction(s)
	s.LoopSelectedCheck.SetChecked(false)
	s.resetRadioSelection(station)
	s.MediaText.SetText(station.StreamURL())
	s.mediafile = nil
	s.TranscodeCheckBox.SetChecked(transcode)
}

func (s *FyneScreen) previewRadioArtwork(*metadata.ArtworkAsset) {}

func (s *FyneScreen) radioTemporaryMode() bool { return false }
