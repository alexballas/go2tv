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
  exit 1
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
