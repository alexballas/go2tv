package utils

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// SubtitlesReaderToSRT prepares a sidecar for DLNA receiver captions.
func SubtitlesReaderToSRT(ctx context.Context, source io.Reader, extension, ffmpegPath string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stopClosing := closeSubtitleSourceOnCancel(ctx, source)
	defer stopClosing()
	switch strings.ToLower(extension) {
	case ".srt":
		return io.ReadAll(source)
	case ".vtt":
		data, err := io.ReadAll(source)
		if err != nil {
			return nil, fmt.Errorf("read WebVTT captions: %w", err)
		}
		return webVTTToSRT(ctx, string(data))
	case ".ass", ".ssa":
		return ConvertASSReaderToSRT(ctx, source, ffmpegPath)
	default:
		return nil, fmt.Errorf("unsupported subtitle format: %s", extension)
	}
}

func webVTTToSRT(ctx context.Context, input string) ([]byte, error) {
	input = strings.TrimPrefix(input, "\ufeff")
	input = strings.ReplaceAll(strings.ReplaceAll(input, "\r\n", "\n"), "\r", "\n")
	blocks := strings.Split(input, "\n\n")
	var output strings.Builder
	cues := 0
	for _, block := range blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lines := strings.Split(strings.TrimSpace(block), "\n")
		if len(lines) == 0 || lines[0] == "" {
			continue
		}
		first := strings.TrimSpace(lines[0])
		if strings.HasPrefix(first, "WEBVTT") || strings.HasPrefix(first, "NOTE") || first == "STYLE" || first == "REGION" {
			continue
		}
		timingIndex := 0
		if !strings.Contains(first, "-->") && len(lines) > 1 {
			timingIndex = 1 // Optional cue identifier.
		}
		if timingIndex >= len(lines) || timingIndex+1 >= len(lines) {
			continue
		}
		match := webVTTCueTiming.FindStringSubmatch(strings.TrimSpace(lines[timingIndex]))
		if match == nil {
			continue
		}
		start := webVTTMilliseconds(match[1])
		end := webVTTMilliseconds(match[2])
		if end <= start {
			continue
		}
		body := strings.TrimSpace(strings.Join(lines[timingIndex+1:], "\n"))
		if body == "" {
			continue
		}
		cues++
		fmt.Fprintf(&output, "%d\n%s --> %s\n%s\n\n", cues, srtTime(start), srtTime(end), body)
	}
	if cues == 0 && strings.TrimSpace(input) != "" {
		return nil, fmt.Errorf("WebVTT has no usable captions")
	}
	return []byte(output.String()), nil
}

func srtTime(milliseconds int64) string {
	return fmt.Sprintf("%02d:%02d:%02d,%03d", milliseconds/3600000, milliseconds/60000%60, milliseconds/1000%60, milliseconds%1000)
}
