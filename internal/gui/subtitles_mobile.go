//go:build android || ios

package gui

import (
	"fmt"
	"path/filepath"

	"github.com/alexballas/refyne/v2/storage"

	"go2tv.app/go2tv/v2/utils"
)

func registerMobileChromecastSubtitles(screen *FyneScreen, host string, offset int, transcode bool) (string, error) {
	if screen.httpserver == nil {
		return "", nil
	}
	screen.httpserver.RemoveHandler("/subtitles.vtt")
	if host == "" || !hasChromecastMobileSubtitles(screen) || (transcode && screen.castBurnSubtitles) {
		return "", nil
	}
	if !transcode {
		offset = 0
	}
	source, err := storage.Reader(screen.subsfile)
	if err != nil {
		return "", fmt.Errorf("open subtitles: %w", err)
	}
	defer source.Close()
	data, err := utils.SubtitlesReaderForPlayback(source, filepath.Ext(screen.SubsText.Text), offset, screen.ffmpegPath)
	if err != nil {
		return "", fmt.Errorf("subtitle conversion: %w", err)
	}
	screen.httpserver.AddHandler("/subtitles.vtt", nil, nil, data)
	return "http://" + host + "/subtitles.vtt", nil
}
