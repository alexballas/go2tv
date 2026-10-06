package utils

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var webVTTCueTiming = regexp.MustCompile(`(?m)^((?:\d{2,}:)?\d{2}:\d{2}\.\d{3})[\t ]+-->[\t ]+((?:\d{2,}:)?\d{2}:\d{2}\.\d{3})([^\n]*)$`)

// SubtitlesForPlayback returns WebVTT with cues relative to the start of a
// transcoded stream. Native playback uses a zero seek offset.
func SubtitlesForPlayback(path string, seekSeconds int, ffmpegPath ...string) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".srt" && ext != ".vtt" && !styledSubtitlePath(path) {
		return nil, fmt.Errorf("unsupported subtitle format: %s", ext)
	}
	source, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	return SubtitlesReaderForPlayback(source, filepath.Ext(path), seekSeconds, ffmpegPath...)
}

// SubtitlesReaderForPlayback supports files and mobile document providers.
// Each call reads the original captions, so repeated seeks never compound offsets.
func SubtitlesReaderForPlayback(source io.Reader, extension string, seekSeconds int, ffmpegPath ...string) ([]byte, error) {
	var data []byte
	var err error
	switch strings.ToLower(extension) {
	case ".srt":
		data, err = ConvertSRTReaderToWebVTT(source)
	case ".vtt":
		data, err = io.ReadAll(source)
	case ".ass", ".ssa":
		path := ""
		if len(ffmpegPath) > 0 {
			path = ffmpegPath[0]
		}
		data, err = convertASSReaderToWebVTT(source, path)
	default:
		return nil, fmt.Errorf("unsupported subtitle format: %s", extension)
	}
	if err != nil || seekSeconds <= 0 {
		return data, err
	}

	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	blocks := strings.Split(text, "\n\n")
	kept := make([]string, 0, len(blocks))
	offset := int64(seekSeconds) * 1000
	for _, block := range blocks {
		// Comments and style blocks may contain text resembling cue timings.
		if strings.HasPrefix(block, "NOTE") || strings.HasPrefix(block, "STYLE") || strings.HasPrefix(block, "REGION") {
			kept = append(kept, block)
			continue
		}
		match := webVTTCueTiming.FindStringSubmatchIndex(block)
		if match != nil {
			start := webVTTMilliseconds(block[match[2]:match[3]]) - offset
			end := webVTTMilliseconds(block[match[4]:match[5]]) - offset
			if end <= 0 {
				continue
			}
			timing := webVTTTimestamp(max(start, 0)) + " --> " + webVTTTimestamp(end) + block[match[6]:match[7]]
			block = block[:match[0]] + timing + block[match[1]:]
		}
		kept = append(kept, block)
	}
	return []byte(strings.Join(kept, "\n\n")), nil
}

func convertASSReaderToWebVTT(source io.Reader, ffmpegPath string) ([]byte, error) {
	path, err := ResolveFFmpegPath(ffmpegPath)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "-nostdin", "-v", "error", "-f", "ass", "-i", "pipe:0", "-map", "0:s:0", "-f", "webvtt", "pipe:1")
	setSysProcAttr(command)
	command.Stdin = source
	data, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("convert ASS captions: %w", err)
	}
	return data, nil
}

func webVTTMilliseconds(timestamp string) int64 {
	parts := strings.FieldsFunc(timestamp, func(r rune) bool { return r == ':' || r == '.' })
	var seconds int64
	for _, part := range parts[:len(parts)-1] {
		value, _ := strconv.ParseInt(part, 10, 64)
		seconds = seconds*60 + value
	}
	milliseconds, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	return seconds*1000 + milliseconds
}

func webVTTTimestamp(milliseconds int64) string {
	return fmt.Sprintf("%02d:%02d:%02d.%03d", milliseconds/3600000, milliseconds/60000%60, milliseconds/1000%60, milliseconds%1000)
}
