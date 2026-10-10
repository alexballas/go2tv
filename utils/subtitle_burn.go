package utils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	maxSubtitleFonts     = 128
	maxSubtitleFontSize  = 16 << 20
	maxSubtitleFontBytes = 64 << 20
)

// EmbeddedSubtitleForBurn identifies styled text and bitmap tracks that must
// be rendered without an SRT conversion. Plain text keeps the existing path.
func EmbeddedSubtitleForBurn(ffmpegPath, path string, track int) (*EmbeddedSubtitle, error) {
	return EmbeddedSubtitleForBurnContext(context.Background(), ffmpegPath, path, track)
}

func EmbeddedSubtitleForBurnContext(ctx context.Context, ffmpegPath, path string, track int) (*EmbeddedSubtitle, error) {
	result, err := probeSubtitleTrackContext(ctx, ffmpegPath, path, track)
	if err != nil {
		return nil, err
	}
	if len(result.Streams) == 1 {
		codec := result.Streams[0].CodecName
		bitmap := bitmapSubtitleCodec(codec)
		if codec == "ass" || codec == "ssa" || bitmap {
			selected := &EmbeddedSubtitle{Path: path, Track: track, Bitmap: bitmap}
			if !bitmap && result.Format.StartTime != "" {
				selected.StartTime, err = strconv.ParseFloat(result.Format.StartTime, 64)
				if err != nil || math.IsNaN(selected.StartTime) || math.IsInf(selected.StartTime, 0) {
					return nil, fmt.Errorf("invalid embedded subtitle timeline origin: %q", result.Format.StartTime)
				}
			}
			return selected, nil
		}
	}
	return nil, nil
}

func styledSubtitlePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ass", ".ssa":
		return true
	}
	return false
}

// libass reads every file in fontsdir. Stage only adjacent fonts so a sidecar
// beside videos never causes the media library to be loaded as font data.
func copySubtitleFonts(ctx context.Context, sourceDir string) (dir string, err error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil && dir != "" {
			_ = os.RemoveAll(dir)
		}
	}()
	count, total := 0, int64(0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return dir, err
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".ttf", ".otf", ".ttc", ".otc", ".woff", ".woff2":
		default:
			continue
		}
		path := filepath.Join(sourceDir, entry.Name())
		info, err := os.Stat(path)
		if err != nil {
			return dir, err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			continue
		}
		total += info.Size()
		if count >= maxSubtitleFonts || info.Size() > maxSubtitleFontSize || total > maxSubtitleFontBytes {
			return dir, fmt.Errorf("oversized subtitle fonts")
		}
		if dir == "" {
			dir, err = os.MkdirTemp("", "go2tv-subtitle-fonts-*")
			if err != nil {
				return dir, err
			}
		}
		input, err := os.Open(path)
		if err != nil {
			return dir, err
		}
		output, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("font-%03d%s", count, ext)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			_ = input.Close()
			return dir, err
		}
		_, copyErr := io.CopyN(output, input, info.Size())
		inputErr, outputErr := input.Close(), output.Close()
		if err := errors.Join(copyErr, inputErr, outputErr); err != nil {
			return dir, err
		}
		count++
	}
	return dir, nil
}
