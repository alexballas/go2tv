//go:build !(android || ios)

package gui

import (
	"path/filepath"
	"testing"
)

func TestConfiguredFFmpegPathKeepsInvalidPreference(t *testing.T) {
	pref := filepath.Join(t.TempDir(), "missing-ffmpeg")

	if got := configuredFFmpegPath(pref); got != pref {
		t.Fatalf("configuredFFmpegPath() = %q, want %q", got, pref)
	}
}

func TestFFmpegDirDisplayPathKeepsInvalidPreference(t *testing.T) {
	pref := filepath.Join(t.TempDir(), "missing-ffmpeg")

	if got := ffmpegDirDisplayPath(pref); got != filepath.ToSlash(pref) {
		t.Fatalf("ffmpegDirDisplayPath() = %q, want %q", got, filepath.ToSlash(pref))
	}
}
