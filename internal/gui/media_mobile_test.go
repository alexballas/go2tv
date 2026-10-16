//go:build android || ios

package gui

import (
	"testing"

	"github.com/alexballas/refyne/v2/storage"
)

func TestMobileFileSelectionLeavesURLMode(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	old := storage.NewFileURI("/old.mp4")
	s.ExternalMediaURL.SetChecked(true)
	s.MediaText.SetText("http://radio.example/stream")
	// Mirrors mainWindow: leaving URL mode restores the previous file.
	s.ExternalMediaURL.OnChanged = func(b bool) {
		if !b {
			s.MediaText.SetText(old.Name())
			s.mediafile = old
		}
	}

	picked := storage.NewFileURI("/picked.mp4")
	setMobileMediaURI(s, picked)

	if s.ExternalMediaURL.Checked {
		t.Fatal("URL mode still enabled")
	}
	if s.mediafile != picked || s.MediaText.Text != picked.Name() {
		t.Fatalf("selection = %v %q, want %v", s.mediafile, s.MediaText.Text, picked)
	}
}
