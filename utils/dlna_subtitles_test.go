package utils

import (
	"context"
	"strings"
	"testing"
)

func TestWebVTTToDLNASRTWithoutFFmpeg(t *testing.T) {
	vtt := "\ufeffWEBVTT\r\n\r\nSTYLE\r\n::cue { color: yellow; }\r\n\r\nNOTE receiver should not see this\r\nprivate\r\n\r\nintro\r\n00:05.000 --> 00:08.500 align:start\r\nFirst caption\r\n\r\n01:02:03.250 --> 01:02:05.000\r\nSecond caption\r\n"
	got, err := SubtitlesReaderToSRT(context.Background(), strings.NewReader(vtt), ".vtt", "/missing/ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1\n00:00:05,000 --> 00:00:08,500\nFirst caption", "2\n01:02:03,250 --> 01:02:05,000\nSecond caption"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	for _, invalid := range []string{"WEBVTT", "STYLE", "NOTE", "align:start"} {
		if strings.Contains(string(got), invalid) {
			t.Fatalf("VTT-only content %q in %q", invalid, got)
		}
	}
}
