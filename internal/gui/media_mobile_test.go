//go:build android || ios

package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexballas/refyne/v2/storage"
)

func TestMobileMediaChangeClearsSubtitles(t *testing.T) {
	tt := []struct {
		name                      string
		sameMedia, initial, clear bool
	}{
		{name: "different video"},
		{name: "clear media", clear: true},
		{name: "same video", sameMedia: true},
		{name: "subtitle selected before media", initial: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s := newMobileTorrentTestScreen(t)
			current := storage.NewFileURI("/sdcard/first.mp4")
			if !tc.initial {
				s.mediafile = current
			}
			s.subsfile = storage.NewFileURI("/sdcard/first.srt")
			s.SubsText.SetText("first.srt")
			temp := filepath.Join(t.TempDir(), "burn.srt")
			if err := os.WriteFile(temp, []byte("first show"), 0600); err != nil {
				t.Fatal(err)
			}
			s.tempSubsFile = temp
			switch {
			case tc.clear:
				clearmediaAction(s)
			case tc.sameMedia:
				setMobileMediaURI(s, storage.NewFileURI(current.Path()))
			default:
				setMobileMediaURI(s, storage.NewFileURI("/sdcard/second.mp4"))
			}
			keep := tc.sameMedia || tc.initial
			if (s.subsfile != nil) != keep || (s.SubsText.Text != "") != keep {
				t.Fatalf("subtitle selection = %v, label = %q; keep = %v", s.subsfile, s.SubsText.Text, keep)
			}
			_, err := os.Stat(temp)
			if keep && err != nil || !keep && !os.IsNotExist(err) {
				t.Fatalf("burn subtitle retained = %v; keep = %v", err == nil, keep)
			}
		})
	}
}

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
