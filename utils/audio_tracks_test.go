package utils

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestParseAudioTracksLabelsAndDefault(t *testing.T) {
	output := []byte(`{"streams":[
		{"codec_name":"ac3","channels":6,"disposition":{"default":0},"tags":{"language":"jpn"}},
		{"codec_name":"eac3","channels":2,"disposition":{"default":1},"tags":{"language":"eng","title":"Commentary"}},
		{"codec_name":"aac","channels":2,"disposition":{"default":0},"tags":{"language":"und"}}
	]}`)
	tracks, err := parseAudioTracks(output)
	if err != nil {
		t.Fatalf("parseAudioTracks: %v", err)
	}
	tt := []struct {
		number int
		label  string
	}{
		{1, "Japanese · AC3 5.1"},
		{2, "Commentary (English) · E-AC3 Stereo"},
		{3, "Track 3 · AAC Stereo"},
	}
	for _, tc := range tt {
		if got := tracks[tc.number-1].Label(); got != tc.label {
			t.Fatalf("track %d label = %q, want %q", tc.number, got, tc.label)
		}
	}
	if got := DefaultAudioTrack(tracks); got != 2 {
		t.Fatalf("default track = %d, want 2", got)
	}
	if _, err := parseAudioTracks([]byte(`{"streams":[]}`)); err != ErrNoAudioTracks {
		t.Fatalf("empty streams err = %v, want ErrNoAudioTracks", err)
	}
}

func TestAudioTrackOverrideSkipsSourceDefault(t *testing.T) {
	tracks := []AudioTrack{{Number: 1}, {Number: 2, Default: true}, {Number: 3}}
	tt := []struct {
		name   string
		chosen int
		want   int
	}{
		{"source default direct-plays", 2, 0},
		{"other track re-streams", 1, 1},
		{"unset", 0, 0},
		{"stale index from previous media", 7, 0},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := AudioTrackOverride(tracks, tc.chosen); got != tc.want {
				t.Fatalf("AudioTrackOverride(%d) = %d, want %d", tc.chosen, got, tc.want)
			}
		})
	}
}

// fakeTrackFFmpeg records each invocation's args.
func fakeTrackFFmpeg(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake ffmpeg test skipped on windows")
	}
	dir := t.TempDir()
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	argsPath := filepath.Join(dir, "args")
	script := `#!/bin/sh
if [ "$1" = "-hide_banner" ] && [ "$2" = "-encoders" ]; then
  echo " V..... libx264 fake"
  exit 0
fi
echo "$*" >> "` + argsPath + `"
`
	if err := os.WriteFile(ffmpegPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return ffmpegPath, argsPath
}

func trackInvocation(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("ffmpeg calls = %q, want 1", lines)
	}
	return strings.Fields(lines[0])
}

func TestSelectedAudioTrackTranscodes(t *testing.T) {
	tt := []struct {
		name  string
		serve func(*TranscodeOptions) error
	}{
		{"dlna", func(opts *TranscodeOptions) error {
			var command exec.Cmd
			return ServeDLNATranscodedStream(context.Background(), &bytes.Buffer{}, "movie.mkv", &command, opts)
		}},
		{"chromecast", func(opts *TranscodeOptions) error {
			var command exec.Cmd
			return ServeChromecastTranscodedStream(context.Background(), &bytes.Buffer{}, "movie.mkv", &command, opts)
		}},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			ffmpegPath, argsPath := fakeTrackFFmpeg(t)
			if err := tc.serve(&TranscodeOptions{FFmpegPath: ffmpegPath, AudioTrack: 2, SeekSeconds: 12}); err != nil {
				t.Fatalf("serve: %v", err)
			}
			args := trackInvocation(t, argsPath)
			joined := " " + strings.Join(args, " ") + " "
			// Video is re-encoded alongside the chosen audio to keep A/V in sync.
			if !strings.Contains(joined, " -map 0:a:1 ") || strings.Contains(joined, " -c:v copy ") || !slices.Contains(args, "libx264") {
				t.Fatalf("args do not transcode audio track 2: %q", args)
			}
			if seek := slices.Index(args, "-ss"); seek < 0 || args[seek+1] != "12" {
				t.Fatalf("lost seek offset: %q", args)
			}
		})
	}
}

func TestDefaultAudioKeepsFFmpegSelection(t *testing.T) {
	ffmpegPath, argsPath := fakeTrackFFmpeg(t)
	var command exec.Cmd
	if err := ServeDLNATranscodedStream(context.Background(), &bytes.Buffer{}, "movie.mkv", &command, &TranscodeOptions{FFmpegPath: ffmpegPath}); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if args := trackInvocation(t, argsPath); slices.Contains(args, "-map") {
		t.Fatalf("default audio must keep FFmpeg's stream choice: %q", args)
	}
}
