package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveFFmpegPathPreferredAbsolute(t *testing.T) {
	dir := t.TempDir()
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	writeExecutableFile(t, ffmpegPath)

	got, err := ResolveFFmpegPath(ffmpegPath)
	if err != nil {
		t.Fatalf("ResolveFFmpegPath() error = %v", err)
	}

	if got != ffmpegPath {
		t.Fatalf("ResolveFFmpegPath() = %q, want %q", got, ffmpegPath)
	}
}

func TestResolveFFprobePathUsesFFmpegSibling(t *testing.T) {
	dir := t.TempDir()
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	ffprobePath := filepath.Join(dir, "ffprobe")
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

func TestResolveFFprobePathUsesPATHForCommandName(t *testing.T) {
	dir := t.TempDir()
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	ffprobePath := filepath.Join(dir, "ffprobe")
	writeExecutableFile(t, ffmpegPath)
	writeExecutableFile(t, ffprobePath)

	oldPath := os.Getenv("PATH")
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})

	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath); err != nil {
		t.Fatalf("Setenv(PATH) error = %v", err)
	}

	got, err := ResolveFFprobePath("ffmpeg")
	if err != nil {
		t.Fatalf("ResolveFFprobePath() error = %v", err)
	}

	if got != ffprobePath {
		t.Fatalf("ResolveFFprobePath() = %q, want %q", got, ffprobePath)
	}
}

func writeExecutableFile(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
