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

func TestServeTranscodedStreamSeekBuildsBufferedPathInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake ffmpeg test skipped on windows")
	}

	dir := t.TempDir()
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	argsPath := filepath.Join(dir, "args")
	script := `#!/bin/sh
has_lavfi=0
for arg in "$@"; do
  if [ "$arg" = "lavfi" ]; then
    has_lavfi=1
  fi
done
if [ "$1" = "-hide_banner" ] && [ "$2" = "-encoders" ]; then
  exit 1
fi
if [ "$has_lavfi" = "1" ]; then
  exit 1
fi
printf '%s\n' "$@" > "$GO2TV_TRANSCODE_ARGS"
`
	if err := os.WriteFile(ffmpegPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO2TV_TRANSCODE_ARGS", argsPath)

	var command exec.Cmd
	if err := ServeTranscodedStream(context.Background(), &bytes.Buffer{}, "movie.mp4", &command, ffmpegPath, "", 37, SubtitleSizeMedium); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(string(data))
	seek := slices.Index(args, "-ss")
	if seek < 0 || seek+1 >= len(args) || args[seek+1] != "37" {
		t.Fatalf("seek args = %q", args)
	}
	if !slices.Contains(args, "-copyts") {
		t.Fatalf("seek did not retain source timestamps: %q", args)
	}
	if slices.Contains(args, "-mpegts_copyts") {
		t.Fatalf("restart seek exposed absolute MPEG-TS timestamps: %q", args)
	}
	if slices.Contains(args, "-re") {
		t.Fatalf("DLNA path transcode cannot build a startup buffer: %q", args)
	}

	if err := ServeTranscodedStream(context.Background(), &bytes.Buffer{}, "movie.mp4", &command, ffmpegPath, "", 0, SubtitleSizeMedium); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	args = strings.Fields(string(data))
	if slices.Contains(args, "-copyts") || slices.Contains(args, "-mpegts_copyts") {
		t.Fatalf("initial DLNA transcode preserved source timestamps: %q", args)
	}
}
