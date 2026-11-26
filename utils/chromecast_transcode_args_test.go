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

func TestChromecastFileTranscodeBuildsStartupBuffer(t *testing.T) {
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
printf '%s\n' "$@" > "$GO2TV_CHROMECAST_ARGS"
`
	if err := os.WriteFile(ffmpegPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO2TV_CHROMECAST_ARGS", argsPath)

	var command exec.Cmd
	opts := &TranscodeOptions{FFmpegPath: ffmpegPath, SeekSeconds: 37}
	if err := ServeChromecastTranscodedStream(context.Background(), &bytes.Buffer{}, "movie.mp4", &command, opts); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(string(data))
	if slices.Contains(args, "-re") {
		t.Fatalf("Chromecast file transcode cannot build a startup buffer: %q", args)
	}
	seek := slices.Index(args, "-ss")
	if seek < 0 || seek+1 >= len(args) || args[seek+1] != "37" || !slices.Contains(args, "-copyts") {
		t.Fatalf("Chromecast seek args = %q", args)
	}
}

// TestChromecastTranscodeFragmentLength: 1s fragments (one per keyframe) made
// the receiver stall for good at ~3s in roughly a third of -tc casts and skips.
// Short-GOP file transcodes need long fragments; live pipes keep fast startup.
func TestChromecastTranscodeFragmentLength(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake ffmpeg test skipped on windows")
	}
	file, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	tt := []struct {
		name     string
		input    any
		hardware bool
		wantMin  bool
	}{
		{name: "hardware local file", input: "movie.mp4", hardware: true, wantMin: true},
		{name: "hardware seekable fd", input: file, hardware: true, wantMin: true},
		{name: "hardware piped stream", input: strings.NewReader("stream"), hardware: true},
		{name: "software long GOP", input: "movie.mp4"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ffmpegPath := filepath.Join(dir, "ffmpeg")
			argsPath := filepath.Join(dir, "args")
			// Record only the transcode itself, not encoder/decoder probes.
			script := `#!/bin/sh
if [ "$1" = "-hide_banner" ] && [ "$2" = "-encoders" ]; then
  echo " V..... libx264 fake"
  exit 0
fi
case " $* " in *" -movflags "*) printf '%s\n' "$@" > "$GO2TV_CHROMECAST_ARGS" ;; esac
`
			if err := os.WriteFile(ffmpegPath, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GO2TV_CHROMECAST_ARGS", argsPath)
			if tc.hardware {
				key := transcodeEncoderCacheKey(ffmpegPath, videoEncoderProfileChromecastFile)
				transcodeVideoEncoderCache.Store(key, transcodeHardwareEncoderPlan(videoEncoderProfileChromecastFile, "h264_v4l2m2m", nil))
				t.Cleanup(func() { transcodeVideoEncoderCache.Delete(key) })
			}
			var command exec.Cmd
			opts := &TranscodeOptions{FFmpegPath: ffmpegPath}
			if err := ServeChromecastTranscodedStream(context.Background(), &bytes.Buffer{}, tc.input, &command, opts); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Fields(string(data))
			i := slices.Index(args, "-min_frag_duration")
			if tc.wantMin && (i < 0 || i+1 >= len(args) || args[i+1] != chromecastMinFragment) {
				t.Fatalf("args = %q, want -min_frag_duration %s", args, chromecastMinFragment)
			}
			if !tc.wantMin && i >= 0 {
				t.Fatalf("args = %q, want no fragment minimum", args)
			}
		})
	}
}
