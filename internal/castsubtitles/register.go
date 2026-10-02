package castsubtitles

import (
	"fmt"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/utils"
)

type Options struct {
	Transcoded       bool
	BurnExternal     bool
	AutomaticTorrent bool
	SeekSeconds      int
}

type Captions struct {
	SubtitleURL        string
	TorrentSubtitleURL string
	BurnPath           string
}

// Register prepares CLI captions from the selected source. External captions
// take precedence; burn-in applies only to external captions on transcoded video.
func Register(server *httphandlers.HTTPserver, host, mediaPath, subtitlePath string, opts Options) (Captions, error) {
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
	captions.TorrentSubtitleURL = RegisterTorrent(server, host, mediaPath, opts.AutomaticTorrent && !external, offset)
	if !external {
		return captions, nil
	}
	if opts.Transcoded && opts.BurnExternal {
		captions.BurnPath = subtitlePath
		return captions, nil
	}
	data, err := utils.SubtitlesForPlayback(subtitlePath, offset)
	if err != nil {
		return captions, fmt.Errorf("subtitle conversion: %w", err)
	}
	server.AddHandler("/subtitles.vtt", nil, nil, data)
	captions.SubtitleURL = "http://" + host + "/subtitles.vtt"
	return captions, nil
}
