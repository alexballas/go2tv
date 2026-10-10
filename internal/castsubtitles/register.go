package castsubtitles

import (
	"context"
	"fmt"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/utils"
)

type Options struct {
	FFmpegPath       string
	Transcoded       bool
	BurnSubtitles    bool
	AutomaticTorrent bool
	SeekSeconds      int
}

type Captions struct {
	SubtitleURL        string
	TorrentSubtitleURL string
	BurnPath           string
	BurnSource         mediasource.Source
}

// Register prepares CLI captions from the selected source. External captions
// take precedence over embedded torrent captions, including during burn-in.
func Register(server *httphandlers.HTTPserver, host, mediaPath, subtitlePath string, opts Options) (Captions, error) {
	return RegisterContext(context.Background(), server, host, mediaPath, subtitlePath, opts)
}

// RegisterContext prepares captions with cancellable external conversion.
func RegisterContext(ctx context.Context, server *httphandlers.HTTPserver, host, mediaPath, subtitlePath string, opts Options) (Captions, error) {
	var captions Captions
	if server == nil || host == "" {
		return captions, nil
	}
	server.RemoveHandler("/subtitles.vtt")
	subtitlePath, external := playback.ChromecastSubtitlePath(subtitlePath)
	offset := 0
	if opts.Transcoded {
		offset = opts.SeekSeconds
	}
	burn := opts.Transcoded && opts.BurnSubtitles
	captions.BurnSource = TorrentSource(mediaPath, opts.AutomaticTorrent && !external && burn)
	captions.TorrentSubtitleURL = RegisterTorrent(server, host, mediaPath, opts.AutomaticTorrent && !external && captions.BurnSource == nil, offset, opts.Transcoded)
	if !external {
		return captions, nil
	}
	if burn {
		captions.BurnPath = subtitlePath
		return captions, nil
	}
	data, err := utils.SubtitlesForPlaybackContext(ctx, subtitlePath, offset, opts.FFmpegPath)
	if err != nil {
		return captions, fmt.Errorf("subtitle conversion: %w", err)
	}
	server.AddHandler("/subtitles.vtt", nil, nil, data)
	captions.SubtitleURL = "http://" + host + "/subtitles.vtt"
	return captions, nil
}
