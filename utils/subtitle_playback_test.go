package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubtitlesForPlayback(t *testing.T) {
	tt := []struct {
		name, ext, input, want string
		seek                   int
	}{
		{name: "native SRT", ext: ".srt", input: "1\n00:00:08,000 --> 00:00:12,000\nHello\n", want: "WEBVTT\n\n1\n00:00:08.000 --> 00:00:12.000\nHello\n"},
		{name: "seek removes expired cues and keeps overlapping cue", ext: ".srt", seek: 10, input: "1\n00:00:01,000 --> 00:00:10,000\nExpired\n\n2\n00:00:08,000 --> 00:00:12,000\nOverlapping\n\n3\n00:00:15,250 --> 00:00:18,500\nLater\n", want: "WEBVTT\n\n2\n00:00:00.000 --> 00:00:02.000\nOverlapping\n\n3\n00:00:05.250 --> 00:00:08.500\nLater\n"},
		{name: "VTT settings and styles survive seek", ext: ".vtt", seek: 60, input: "WEBVTT\r\n\r\nSTYLE\r\n::cue { color: lime; }\r\n\r\ncaption\r\n01:01.250 --> 01:03.500 align:start\r\nHello\r\n", want: "WEBVTT\n\nSTYLE\n::cue { color: lime; }\n\ncaption\n00:00:01.250 --> 00:00:03.500 align:start\nHello\n"},
		{name: "native VTT untouched", ext: ".vtt", input: "WEBVTT\n\n00:01.000 --> 00:02.000\nHello\n", want: "WEBVTT\n\n00:01.000 --> 00:02.000\nHello\n"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "subtitles"+tc.ext)
			if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := SubtitlesForPlayback(path, tc.seek)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("missing file reports error", func(t *testing.T) {
		if _, err := SubtitlesForPlayback(filepath.Join(t.TempDir(), "missing.srt"), 0); err == nil {
			t.Fatal("missing file accepted")
		}
	})
	t.Run("unsupported format reports error", func(t *testing.T) {
		if _, err := SubtitlesForPlayback("captions.ass", 0); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("error = %v", err)
		}
	})
}
