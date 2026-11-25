package httphandlers

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"go2tv.app/go2tv/v2/utils"
)

// PrepareDLNASubtitles makes direct-play receiver captions available as SRT.
// ASS override blocks are stripped from the served body. On open/convert
// failure it falls back to casting without captions (URL path "/.") instead
// of aborting the cast. Transcoded callers keep the original source so
// FFmpeg can burn ASS styling.
func PrepareDLNASubtitles(ctx context.Context, subtitleURL string, subtitles any, ffmpegPath string) (string, any, error) {
	parsed, err := url.Parse(subtitleURL)
	if err != nil {
		return "", nil, fmt.Errorf("parse subtitle URL: %w", err)
	}
	extension := strings.ToLower(filepath.Ext(parsed.Path))
	if subtitles == nil || extension == "" {
		return subtitleURL, subtitles, nil
	}
	if path, ok := subtitles.(string); ok && path == "" {
		return subtitleURL, subtitles, nil
	}
	switch extension {
	case ".srt", ".ass", ".ssa", ".vtt":
	default:
		return subtitleURL, subtitles, nil
	}

	// disableCaptions rewrites the URL to the established no-caption path.
	disableCaptions := func() (string, any, error) {
		parsed.Path = "/."
		parsed.RawPath = ""
		return parsed.String(), nil, nil
	}

	var source io.Reader
	switch value := subtitles.(type) {
	case string:
		file, openErr := os.Open(value)
		if openErr != nil {
			return disableCaptions()
		}
		defer file.Close()
		source = file
	case io.ReadCloser:
		defer value.Close()
		source = value
	case io.Reader:
		source = value
	default:
		return "", nil, fmt.Errorf("unsupported subtitle source %T", subtitles)
	}
	converted, err := utils.SubtitlesReaderToSRT(ctx, source, extension, ffmpegPath)
	if err != nil {
		return disableCaptions()
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, filepath.Ext(parsed.Path)) + ".srt"
	parsed.RawPath = ""
	return parsed.String(), converted, nil
}
