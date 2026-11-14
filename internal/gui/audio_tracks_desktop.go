//go:build !(android || ios)

package gui

import (
	"context"

	"go2tv.app/go2tv/v2/utils"
)

// audioTrackMedia identifies a local or torrent video whose audio tracks can
// be chosen, with its probe. Other sources return "".
func (s *FyneScreen) audioTrackMedia() (string, func(context.Context) ([]utils.AudioTrack, error)) {
	if s.mediafile == "" || s.Screencast || (s.ExternalMediaURL != nil && s.ExternalMediaURL.Checked) ||
		(s.rtmpServerCheck != nil && s.rtmpServerCheck.Checked) || s.mediaKindForPath(s.mediafile) != "video" {
		return "", nil
	}
	path, ffmpeg := s.mediafile, s.ffmpegPath
	return path, func(ctx context.Context) ([]utils.AudioTrack, error) {
		return utils.AudioTracksContext(ctx, ffmpeg, path)
	}
}

// audioTranscodeReverted re-checks Chromecast compatibility, which may need
// Transcode regardless of the audio track.
func (s *FyneScreen) audioTranscodeReverted() {
	s.chromecastCheckedFile = ""
	s.checkChromecastCompatibility()
}
