package utils

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestResolveFFmpegPathUsesPlatformHookForDefault(t *testing.T) {
	oldHook := platformFFmpegPath
	t.Cleanup(func() {
		platformFFmpegPath = oldHook
	})

	ffmpegPath := filepath.Join(t.TempDir(), "libffmpeg.so")
	platformFFmpegPath = func() (string, error) {
		return ffmpegPath, nil
	}

	got, err := ResolveFFmpegPath("")
	if err != nil {
		t.Fatalf("ResolveFFmpegPath() error = %v", err)
	}

	if got != ffmpegPath {
		t.Fatalf("ResolveFFmpegPath() = %q, want %q", got, ffmpegPath)
	}
}

func TestResolveFFmpegPathPreferredBypassesPlatformHook(t *testing.T) {
	oldHook := platformFFmpegPath
	t.Cleanup(func() {
		platformFFmpegPath = oldHook
	})

	platformFFmpegPath = func() (string, error) {
		return filepath.Join(t.TempDir(), "libffmpeg.so"), nil
	}

	ffmpegPath := filepath.Join(t.TempDir(), "ffmpeg")
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
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe" // A portable FFmpeg folder outside PATH.
	}
	ffmpegPath := filepath.Join(dir, "ffmpeg"+exe)
	ffprobePath := filepath.Join(dir, "ffprobe"+exe)
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

func TestResolveFFprobePathUsesPlatformHook(t *testing.T) {
	oldHook := platformFFprobePath
	t.Cleanup(func() {
		platformFFprobePath = oldHook
	})

	ffprobePath := filepath.Join(t.TempDir(), "libffprobe.so")
	platformFFprobePath = func() (string, error) {
		return ffprobePath, nil
	}

	got, err := ResolveFFprobePath(filepath.Join(t.TempDir(), "libffmpeg.so"))
	if err != nil {
		t.Fatalf("ResolveFFprobePath() error = %v", err)
	}

	if got != ffprobePath {
		t.Fatalf("ResolveFFprobePath() = %q, want %q", got, ffprobePath)
	}
}

func TestResolveFFprobePathPreferredBypassesPlatformHook(t *testing.T) {
	oldHook := platformFFprobePath
	t.Cleanup(func() {
		platformFFprobePath = oldHook
	})

	platformFFprobePath = func() (string, error) {
		return filepath.Join(t.TempDir(), "libffprobe.so"), nil
	}

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

func TestResolveFFmpegPathPreferredDirectory(t *testing.T) {
	dir := t.TempDir()
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	writeExecutableFile(t, ffmpegPath)

	got, err := ResolveFFmpegPath(dir)
	if err != nil {
		t.Fatalf("ResolveFFmpegPath() error = %v", err)
	}

	if got != ffmpegPath {
		t.Fatalf("ResolveFFmpegPath() = %q, want %q", got, ffmpegPath)
	}
}

func TestResolveFFmpegPathPrefersDir(t *testing.T) {
	dir := t.TempDir()
	otherDir := t.TempDir()
	otherFFmpegPath := filepath.Join(otherDir, "ffmpeg")
	writeExecutableFile(t, otherFFmpegPath)

	oldPath := os.Getenv("PATH")
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})

	if err := os.Setenv("PATH", otherDir); err != nil {
		t.Fatalf("Setenv(PATH) error = %v", err)
	}

	if _, err := ResolveFFmpegPath(dir); err == nil {
		t.Fatalf("ResolveFFmpegPath(%q) error = nil, want error", dir)
	}
}

func writeExecutableFile(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
