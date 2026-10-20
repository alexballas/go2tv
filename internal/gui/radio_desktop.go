//go:build !(android || ios)

package gui

import (
	"bytes"
	"context"
	"errors"
	"image"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/metadata"
)

func (s *FyneScreen) validateRadioSelection() error {
	if s.radioTemporaryMode() {
		return errors.New("stop screencast or RTMP before selecting radio")
	}
	return nil
}

func (s *FyneScreen) cancelQueuedRadioPlayback() { s.cancelPendingTorrentPlayback() }

func (s *FyneScreen) playRadioStation() { playAction(s) }

func (s *FyneScreen) replaceRadioStation() {
	s.cancelDLNAFollowup()
	playActionOnTargetContext(s, selectedPlaybackTarget(s), context.Background(), true)
}

func (s *FyneScreen) radioSongDisplayAvailable() bool { return s.playbackStatus != nil }

func (s *FyneScreen) refreshRadioSongView() {
	s.refreshPlaybackReadiness()
	if s.mpris != nil {
		s.mpris.refresh()
	}
}

func (s *FyneScreen) selectRadioStation(station radiobrowser.Station) {
	transcode := s.TranscodeCheckBox.Checked
	if torrentMediaSelected(s) {
		cancelTorrent(s)
		clearCurrentMediaSelection(s)
	}
	s.ExternalMediaURL.SetChecked(true)
	s.NextMediaCheck.SetChecked(false)
	s.LoopSelectedCheck.SetChecked(false)
	s.CustomSubsCheck.SetChecked(false)
	clearsubsAction(s)
	s.resetRadioSelection(station)
	s.MediaText.SetText(station.StreamURL())
	s.mediafile = station.StreamURL()
	s.TranscodeCheckBox.SetChecked(transcode)
	if s.mediaSelection != nil {
		s.mediaSelection.refresh()
	}
}

func (s *FyneScreen) previewRadioArtwork(asset *metadata.ArtworkAsset) {
	if s.selectedArtwork == nil {
		return
	}
	s.selectArtwork("")
	if asset == nil {
		return
	}
	decoded, _, err := image.Decode(bytes.NewReader(asset.Data))
	if err != nil {
		s.logRadioError("preview artwork", err)
		return
	}
	s.selectedArtwork.applyArtwork(context.Background(), decoded)
}

func (s *FyneScreen) radioTemporaryMode() bool {
	return s.Screencast || (s.rtmpServerCheck != nil && s.rtmpServerCheck.Checked)
}
