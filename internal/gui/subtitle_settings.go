package gui

import (
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/lang"
)

const chromecastBurnSubtitlesPref = "BurnChromecastSubtitles"

func newChromecastSubtitleSettings(screen *FyneScreen) *wrappingCheck {
	prefs := fyne.CurrentApp().Preferences()
	burn := newWrappingCheck(lang.L("Burn Chromecast Subtitles (Compatibility Fallback)"), func(enabled bool) {
		prefs.SetBool(chromecastBurnSubtitlesPref, enabled)
	})
	burn.SetChecked(prefs.BoolWithFallback(chromecastBurnSubtitlesPref, false))
	screen.burnSubtitlesCheck = burn
	screen.updateChromecastSubtitleAvailability(screen.ffmpegStatus())
	return burn
}

// Called on the UI thread, alongside the other FFmpeg-dependent controls.
func (screen *FyneScreen) updateChromecastSubtitleAvailability(ffmpegErr error) {
	burn := screen.burnSubtitlesCheck
	if burn == nil {
		return
	}
	if ffmpegErr != nil {
		burn.SetChecked(false)
		fyne.CurrentApp().Preferences().SetBool(chromecastBurnSubtitlesPref, false)
		burn.Disable()
		burn.SetToolTip(lang.L("ffmpeg is required. install it or update ffmpeg path in Settings"))
		return
	}
	burn.Enable()
	burn.SetToolTip(lang.L("Requires transcoding. Preserves ASS/SSA styling and embedded fonts, including torrent subtitles."))
}

// Capture once per load; changing settings must not change a running seek's mode.
func (screen *FyneScreen) captureChromecastSubtitleSettings() {
	prefs := fyne.CurrentApp().Preferences()
	screen.castBurnSubtitles = prefs.BoolWithFallback(chromecastBurnSubtitlesPref, false)
	if screen.castBurnSubtitles && screen.ffmpegStatus() != nil {
		if screen.playbackStartupContext().Err() != nil {
			return
		}
		screen.castBurnSubtitles = false
		prefs.SetBool(chromecastBurnSubtitlesPref, false)
	}
}
