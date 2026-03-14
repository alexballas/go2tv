//go:build windows

package utils

import (
	"path/filepath"
	"testing"
)

func TestResolveFFmpegPathPreferredWithoutExeSuffix(t *testing.T) {
	dir := t.TempDir()
	ffmpegPath := filepath.Join(dir, "ffmpeg.exe")
	writeExecutableFile(t, ffmpegPath)

	got, err := ResolveFFmpegPath(filepath.Join(dir, "ffmpeg"))
	if err != nil {
		t.Fatalf("ResolveFFmpegPath() error = %v", err)
	}

	if got != ffmpegPath {
		t.Fatalf("ResolveFFmpegPath() = %q, want %q", got, ffmpegPath)
	}
}

func TestResolveFFprobePathUsesWindowsSibling(t *testing.T) {
	dir := t.TempDir()
	ffmpegPath := filepath.Join(dir, "ffmpeg.exe")
	ffprobePath := filepath.Join(dir, "ffprobe.exe")
	writeExecutableFile(t, ffmpegPath)
	writeExecutableFile(t, ffprobePath)

	got, err := ResolveFFprobePath(ffmpegPath)
	if err != nil {
		t.Fatalf("ResolveFFprobePath() error = %v", err)
	}

	if got != ffprobePath {
		t.Fatalf("ResolveFFprobePath() = %q, want %q", got, ffprobePath)
	}
}
