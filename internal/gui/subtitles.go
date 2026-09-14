//go:build !(android || ios)

package gui

import (
	"fmt"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/utils"
)

// registerChromecastSubtitles uses the receiver's text renderer for both direct
// and transcoded playback, keeping styling identical without requiring libass.
func registerChromecastSubtitles(server *httphandlers.HTTPserver, host, path string, seekSeconds int) (string, error) {
	if server == nil || host == "" {
		return "", nil
	}
	server.RemoveHandler("/subtitles.vtt")
	path, ok := playback.ChromecastSubtitlePath(path)
	if !ok {
		return "", nil
	}
	data, err := utils.SubtitlesForPlayback(path, seekSeconds)
	if err != nil {
		return "", fmt.Errorf("subtitle conversion: %w", err)
	}
	server.AddHandler("/subtitles.vtt", nil, nil, data)
	return "http://" + host + "/subtitles.vtt", nil
}
