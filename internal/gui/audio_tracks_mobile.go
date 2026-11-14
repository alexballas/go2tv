//go:build android || ios

package gui

import (
	"context"

	"github.com/alexballas/refyne/v2/storage"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/utils"
)

// audioTrackMedia identifies a picked or torrent video whose audio tracks can
// be chosen, with its probe. Content URIs are probed through their seekable
// descriptor; other sources return "".
func (s *FyneScreen) audioTrackMedia() (string, func(context.Context) ([]utils.AudioTrack, error)) {
	uri := s.mediafile
	if uri == nil || (s.ExternalMediaURL != nil && s.ExternalMediaURL.Checked) ||
		mediamodel.KindForPath(uri.Name()) != mediamodel.MediaKindVideo {
		return "", nil
	}
	ffmpeg := s.ffmpegPath
	return uri.String(), func(ctx context.Context) ([]utils.AudioTrack, error) {
		if _, ok := mediasource.Lookup(uri.Path()); ok {
			return utils.AudioTracksContext(ctx, ffmpeg, uri.Path())
		}
		media, err := storage.ReaderSeeker(uri)
		if err != nil {
			return nil, err
		}
		defer media.Close()
		return utils.AudioTracksReaderContext(ctx, ffmpeg, media)
	}
}

func (s *FyneScreen) audioTranscodeReverted() {}
