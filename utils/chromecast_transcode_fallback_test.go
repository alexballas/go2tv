package utils

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestServeChromecastTranscodedStreamRuntimeHardwareFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake ffmpeg test skipped on windows")
	}

	ffmpegPath := writeFakeRuntimeFallbackFFmpeg(t)
	inputPath := filepath.Join(t.TempDir(), "input.mp4")
	if err := os.WriteFile(inputPath, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	opts := &TranscodeOptions{
		FFmpegPath: ffmpegPath,
	}
	var ff exec.Cmd
	var out bytes.Buffer

	err := ServeChromecastTranscodedStream(context.Background(), &out, inputPath, &ff, opts)
	if err != nil {
		t.Fatalf("expected fallback success, got err: %v", err)
	}
	if out.String() != "software" {
		t.Fatalf("expected software fallback output, got %q", out.String())
	}
}

func writeFakeRuntimeFallbackFFmpeg(t *testing.T) string {
	t.Helper()

	script := `#!/bin/sh
if [ "$1" = "-hide_banner" ] && [ "$2" = "-encoders" ]; then
  echo "Encoders:"
  echo " V..... h264_nvenc           fake"
  exit 0
fi

has_nvenc=0
has_x264=0
has_lavfi=0
for arg in "$@"; do
  if [ "$arg" = "h264_nvenc" ]; then
    has_nvenc=1
  fi
  if [ "$arg" = "libx264" ]; then
    has_x264=1
  fi
  if [ "$arg" = "lavfi" ]; then
    has_lavfi=1
  fi
done

if [ "$has_nvenc" = "1" ]; then
  if [ "$has_lavfi" = "1" ]; then
    exit 0
  fi
  echo "nvenc runtime failed" >&2
  exit 1
fi

if [ "$has_x264" = "1" ]; then
  printf "software"
  exit 0
fi

echo "unknown invocation" >&2
exit 1
`

	path := filepath.Join(t.TempDir(), "fake-ffmpeg-runtime-fallback")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}
