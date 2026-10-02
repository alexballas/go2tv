package mkvsubs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMatroska(t *testing.T, body []byte) string {
	t.Helper()
	// Deliberately unrelated extension: extraction must inspect the container.
	path := filepath.Join(t.TempDir(), "movie.data")
	data := append(elem(0x1a45dfa3, elem(0x4282, []byte("matroska"))), body...)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractFileTrackSelection(t *testing.T) {
	tracks := elem(0x1654ae6b,
		textTrack(2, "S_TEXT/WEBVTT"),
		textTrack(7, "S_TEXT/UTF8"),
		textTrack(11, "S_TEXT/UTF8"))
	path := writeMatroska(t, elem(segmentID, tracks,
		elem(clusterID, number(0xe7, 3600000),
			subBlock(7, -1000, 10000, "First caption"),
			subBlock(11, 1234, 4321, "<i>Second caption</i>\nNext line"))))
	tt := []struct {
		name  string
		index int
		want  string
	}{
		{"unsupported first track does not shift selection", 1, "1\n00:59:59,000 --> 01:00:09,000\nFirst caption\n\n"},
		{"explicit second UTF8 track keeps markup and timing", 2, "1\n01:00:01,234 --> 01:00:05,555\n<i>Second caption</i>\nNext line\n\n"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := ExtractFile(context.Background(), path, tc.index, 0, &out); err != nil {
				t.Fatal(err)
			}
			if out.String() != tc.want {
				t.Fatalf("SRT = %q, want %q", out.String(), tc.want)
			}
		})
	}
}

func TestExtractFileCompleteTrack(t *testing.T) {
	// A long movie exceeds progressive window limits and has captions far
	// beyond the first 30 seconds. File export must retain every caption.
	body := elem(0x1654ae6b, textTrack(7, "S_TEXT/UTF8"))
	const count = 4100
	for i := range count {
		cluster := elem(clusterID, number(0xe7, uint64(i)*1000), subBlock(7, 0, 1000, fmt.Sprintf("Caption %d", i)))
		// Unknown-sized clusters must not swallow later clusters.
		cluster[4] = 0xff
		body = append(body, cluster...)
	}
	path := writeMatroska(t, elem(segmentID, body))
	var out bytes.Buffer
	if err := ExtractFile(context.Background(), path, 0, 0, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), " --> ") != count || !strings.HasSuffix(out.String(), "4100\n01:08:19,000 --> 01:08:20,000\nCaption 4099\n\n") {
		t.Fatalf("incomplete subtitle export: %d cues", strings.Count(out.String(), " --> "))
	}
}

func TestExtractFileRequiresFallback(t *testing.T) {
	tt := []struct {
		name  string
		track []byte
		block []byte
		index int
	}{
		{"ASS styling", textTrack(7, "S_TEXT/ASS"), subBlock(7, 0, 1000, "0,0,Default,,0,0,0,,{\\i1}Italic{\\i0}"), 0},
		{"WebVTT", textTrack(7, "S_TEXT/WEBVTT"), subBlock(7, 0, 1000, "Caption"), 0},
		{"encoded UTF8", elem(0xae, number(0xd7, 7), number(0x83, 17), elem(0x86, []byte("S_TEXT/UTF8")), elem(0x6d80)), subBlock(7, 0, 1000, "Caption"), 0},
		{"codec delay", elem(0xae, number(0xd7, 7), number(0x83, 17), elem(0x86, []byte("S_TEXT/UTF8")), number(0x56aa, 1000000)), subBlock(7, 0, 1000, "Caption"), 0},
		{"missing duration", textTrack(7, "S_TEXT/UTF8"), elem(0xa3, []byte{0x87, 0, 0, 0, 'X'}), 0},
		{"laced UTF8", textTrack(7, "S_TEXT/UTF8"), elem(0xa3, []byte{0x87, 0, 0, 2, 'X'}), 0},
		{"invalid UTF8", textTrack(7, "S_TEXT/UTF8"), subBlock(7, 0, 1000, "\xff"), 0},
		{"negative timestamp", textTrack(7, "S_TEXT/UTF8"), subBlock(7, -100, 1000, "Caption"), 0},
		{"invalid selection", textTrack(7, "S_TEXT/UTF8"), subBlock(7, 0, 1000, "Caption"), 1},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			path := writeMatroska(t, elem(segmentID, elem(0x1654ae6b, tc.track), elem(clusterID, number(0xe7, 0), tc.block)))
			var out bytes.Buffer
			if err := ExtractFile(context.Background(), path, tc.index, 0, &out); err == nil {
				t.Fatalf("unsupported track exported: %q", out.String())
			}
		})
	}
}
