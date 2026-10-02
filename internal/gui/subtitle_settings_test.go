package gui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexballas/refyne/v2/test"
)

func TestChromecastSubtitleSettingPersistsAndAppliesOnNextLoad(t *testing.T) {
	app := test.NewApp()
	t.Cleanup(app.Quit)
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	screen := &FyneScreen{ffmpegPath: ffmpeg}
	screen.captureChromecastSubtitleSettings()
	toggle := newChromecastSubtitleSettings(screen)
	if toggle.Checked || screen.castBurnSubtitles {
		t.Fatal("burn-in enabled by default")
	}
	toggle.SetChecked(true)
	if !app.Preferences().Bool(chromecastBurnSubtitlesPref) || !newChromecastSubtitleSettings(screen).Checked {
		t.Fatal("subtitle fallback preference not persisted")
	}
	if screen.castBurnSubtitles {
		t.Fatal("setting changed the active playback mode")
	}
	screen.captureChromecastSubtitleSettings()
	if !screen.castBurnSubtitles {
		t.Fatal("next load ignored subtitle fallback preference")
	}
	toggle.SetChecked(false)
	if !screen.castBurnSubtitles {
		t.Fatal("disabling fallback changed the active playback mode")
	}
	screen.captureChromecastSubtitleSettings()
	if screen.castBurnSubtitles {
		t.Fatal("next load did not restore receiver captions")
	}
}

func TestChromecastSubtitleFallbackDisabledWithoutFFmpeg(t *testing.T) {
	tt := []struct {
		name   string
		broken bool
	}{
		{name: "missing"},
		{name: "unusable", broken: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			app := test.NewApp()
			t.Cleanup(app.Quit)
			app.Preferences().SetBool(chromecastBurnSubtitlesPref, true)
			path := filepath.Join(t.TempDir(), "ffmpeg")
			if tc.broken {
				if err := os.WriteFile(path, []byte("unusable executable"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			screen := &FyneScreen{ffmpegPath: path}
			toggle := newChromecastSubtitleSettings(screen)
			if !toggle.Disabled() || toggle.Checked || toggle.ToolTip() == "" || app.Preferences().Bool(chromecastBurnSubtitlesPref) {
				t.Fatalf("missing FFmpeg: disabled=%v checked=%v tooltip=%q saved=%v", toggle.Disabled(), toggle.Checked, toggle.ToolTip(), app.Preferences().Bool(chromecastBurnSubtitlesPref))
			}
			test.Tap(toggle)
			if toggle.Checked {
				t.Fatal("disabled fallback could be enabled")
			}
			// Playback must also reject a stale enabled preference without FFmpeg.
			app.Preferences().SetBool(chromecastBurnSubtitlesPref, true)
			screen.captureChromecastSubtitleSettings()
			if screen.castBurnSubtitles || app.Preferences().Bool(chromecastBurnSubtitlesPref) {
				t.Fatal("playback retained burn-in without FFmpeg")
			}
			ffmpeg, err := exec.LookPath("ffmpeg")
			if err != nil {
				return
			}
			screen.ffmpegPath = ffmpeg
			screen.updateChromecastSubtitleAvailability(screen.ffmpegStatus())
			if toggle.Disabled() || toggle.Checked || toggle.ToolTip() != "" {
				t.Fatalf("restored FFmpeg: disabled=%v checked=%v tooltip=%q", toggle.Disabled(), toggle.Checked, toggle.ToolTip())
			}
			test.Tap(toggle)
			if !toggle.Checked || !app.Preferences().Bool(chromecastBurnSubtitlesPref) {
				t.Fatal("restored FFmpeg did not allow fallback")
			}
		})
	}
}
