package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var webVTTCueTiming = regexp.MustCompile(`(?m)^((?:\d{2,}:)?\d{2}:\d{2}\.\d{3})[\t ]+-->[\t ]+((?:\d{2,}:)?\d{2}:\d{2}\.\d{3})([^\n]*)$`)

// SubtitlesForPlayback returns WebVTT with cues relative to the start of a
// transcoded stream. Native playback uses a zero seek offset.
func SubtitlesForPlayback(path string, seekSeconds int) ([]byte, error) {
	var data []byte
	var err error
	switch strings.ToLower(filepath.Ext(path)) {
	case ".srt":
		data, err = ConvertSRTtoWebVTT(path)
	case ".vtt":
		data, err = os.ReadFile(path)
	default:
		return nil, fmt.Errorf("unsupported subtitle format: %s", filepath.Ext(path))
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
