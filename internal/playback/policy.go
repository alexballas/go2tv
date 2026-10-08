package playback

import (
	"os"
	"path/filepath"
	"strings"

	"go2tv.app/go2tv/v2/utils"
)

type ChromecastURLPolicy struct {
	Transcode        bool
	DirectMediaURL   bool
	NeedsLocalServer bool
}

func ChromecastExternalURLPolicy(mediaURL, mediaType string, transcodeRequested bool, subtitlesPath string) ChromecastURLPolicy {
	transcode := ChromecastTranscodeEnabled(transcodeRequested, mediaURL, mediaType)
	_, hasSubtitles := ChromecastSubtitlePath(subtitlesPath)

	return ChromecastURLPolicy{
		Transcode:        transcode,
		DirectMediaURL:   !transcode,
		NeedsLocalServer: transcode || hasSubtitles,
	}
}

func ChromecastTranscodeEnabled(requested bool, mediaURL, mediaType string) bool {
	if !requested {
		return false
	}

	if utils.IsHLSStream(mediaURL, mediaType) {
		return false
	}

	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mediaType)), "video/")
}

func ChromecastSubtitlePath(subtitlesPath string) (string, bool) {
	if subtitlesPath == "" {
		return "", false
	}

	info, err := os.Stat(subtitlesPath)
	if err != nil || info.IsDir() {
		return "", false
	}

	switch strings.ToLower(filepath.Ext(subtitlesPath)) {
	case ".srt", ".vtt", ".ass", ".ssa":
		return subtitlesPath, true
	default:
		return "", false
	}
}
