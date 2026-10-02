package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestEmbeddedSubtitleBackends(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable for fixture creation")
	}
	tt := []struct {
		name, extension, firstCodec, secondCodec string
		native                                   bool
	}{
		{"Matroska UTF8", ".mkv", "srt", "srt", true},
		{"UTF8 follows unsupported WebVTT", ".mkv", "webvtt", "srt", true},
		{"Matroska ASS preserves italics", ".mkv", "ass", "ass", false},
		{"Matroska WebVTT fallback", ".mkv", "webvtt", "webvtt", false},
		{"MP4 timed text fallback", ".mp4", "mov_text", "mov_text", false},
		{"MOV timed text fallback", ".mov", "mov_text", "mov_text", false},
		{"WebM WebVTT fallback", ".webm", "webvtt", "webvtt", false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			captions := []string{"First caption", "<i>Second caption</i>\nNext line"}
			args := []string{"-nostdin", "-loglevel", "error"}
			for i, caption := range captions {
				path := filepath.Join(dir, fmt.Sprintf("%d.srt", i))
				if err := os.WriteFile(path, []byte("1\n00:00:01,234 --> 00:00:05,555\n"+caption+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "-i", path)
			}
			media := filepath.Join(dir, "movie"+tc.extension)
			args = append(args, "-map", "0:s", "-map", "1:s", "-c:s:0", tc.firstCodec, "-c:s:1", tc.secondCodec,
				"-metadata:s:s:0", "title=First", "-metadata:s:s:0", "language=eng",
				"-metadata:s:s:1", "title=Second", "-metadata:s:s:1", "language=ell", media)
			if output, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
				t.Fatalf("create fixture: %v: %s", err, output)
			}
			// The dropdown must retain ffprobe's track count, order and labels.
			names, err := GetSubs(ffmpeg, media)
			if err != nil || len(names) != 2 {
				t.Fatalf("subtitle listing: %v, %v", names, err)
			}
			if tc.extension == ".mkv" && !slices.Equal(names, []string{"First (eng)", "Second (ell)"}) {
				t.Fatalf("subtitle labels/order changed: %v", names)
			}
			tool := ffmpeg
			if tc.native {
				// Supported tracks must extract without launching FFmpeg.
				tool = filepath.Join(dir, "missing-ffmpeg")
			}
			path, err := ExtractSub(tool, 1, media)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(path)
			data, err := SubtitlesForPlayback(path, 0)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !strings.Contains(text, "00:00:01.234 --> 00:00:05.555") || !strings.Contains(text, "<i>Second caption</i>\nNext line") || strings.Contains(text, "First caption") {
				t.Fatalf("selection, timing or formatting changed: %s", text)
			}
			if tc.firstCodec != "srt" {
				// Selecting the preceding unsupported track must use FFmpeg,
				// never silently substitute the later supported track.
				first, err := ExtractSub(ffmpeg, 0, media)
				if err != nil {
					t.Fatal(err)
				}
				defer os.Remove(first)
				data, err := os.ReadFile(first)
				if err != nil || !strings.Contains(string(data), "First caption") || strings.Contains(string(data), "Second caption") {
					t.Fatalf("fallback selected wrong track: %s, %v", data, err)
				}
			}
		})
	}
}

func TestNativeSubtitleTimelineMatchesFFmpeg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable for fixture creation")
	}
	tt := []struct {
		name, offset string
	}{
		{"zero media start", "0"},
		{"nonzero media start", "5"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			subs := filepath.Join(dir, "captions.srt")
			if err := os.WriteFile(subs, []byte("1\n00:00:01,234 --> 00:00:05,555\nCaption\n"), 0600); err != nil {
				t.Fatal(err)
			}
			media := filepath.Join(dir, "movie.mkv")
			cmd := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=size=16x16:duration=1",
				"-i", subs, "-map", "0:v", "-map", "1:s", "-c:v", "mpeg4", "-c:s", "srt", "-output_ts_offset", tc.offset, media)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("create offset fixture: %v: %s", err, output)
			}
			baseline := filepath.Join(dir, "ffmpeg.srt")
			if output, err := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-i", media, "-map", "0:s:0", baseline).CombinedOutput(); err != nil {
				t.Fatalf("extract baseline: %v: %s", err, output)
			}
			path, err := ExtractSub(filepath.Join(dir, "missing-ffmpeg"), 0, media)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(path)
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(baseline)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) || !strings.Contains(string(got), "00:00:01,234 --> 00:00:05,555") {
				t.Fatalf("native timeline %q differs from FFmpeg %q", got, want)
			}
		})
	}
}
