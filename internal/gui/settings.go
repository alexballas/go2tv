//go:build !(android || ios)
// +build !android,!ios

package gui

import (
	"runtime"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func settingsWindow(s *NewScreen) fyne.CanvasObject {
	w := s.Current

	themeText := widget.NewLabel("Theme")
	dropdown := widget.NewSelect([]string{"Light", "Dark"}, parseTheme)
	themeName := fyne.CurrentApp().Preferences().StringWithFallback("Theme", "GrabVariant")
	switch themeName {
	case "Light":
		dropdown.PlaceHolder = "Light"
		parseTheme("Light")
	case "Dark":
		dropdown.PlaceHolder = "Dark"
		parseTheme("Dark")
	case "GrabVariant", "Default":
		fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"GrabVariant"})

		// Wait for the SystemVariant variable to change
		<-signalSystemVariantChange

		switch SystemVariant {
		case theme.VariantDark:
			dropdown.PlaceHolder = "Dark"
			parseTheme("Dark")
		case theme.VariantLight:
			dropdown.PlaceHolder = "Light"
			parseTheme("Light")
		}
	}

	ffmpegText := widget.NewLabel("ffmpeg Path")
	ffmpegTextEntry := widget.NewEntry()

	ffmpegTextEntry.Text = func() string {
		if fyne.CurrentApp().Preferences().String("ffmpeg") != "" {
			return fyne.CurrentApp().Preferences().String("ffmpeg")
		}

		os := runtime.GOOS
		switch os {
		case "windows":
			return "ffmpeg"
		case "linux":
			return "ffmpeg"
		case "darwin":
			return "/opt/homebrew/bin/ffmpeg"
		default:
			return "ffmpeg"
		}

	}()
	ffmpegTextEntry.Refresh()
	s.ffmpegPath = ffmpegTextEntry.Text

	ffmpegTextEntry.OnChanged = func(update string) {
		s.ffmpegPath = update
		fyne.CurrentApp().Preferences().SetString("ffmpeg", update)
	}

	debugText := widget.NewLabel("Debug")
	debugExport := widget.NewButton("Export Debug Logs", func() {
		var itemInRing bool
		s.Debug.ring.Do(func(p interface{}) {
			if p != nil {
				itemInRing = true
			}
		})

		if !itemInRing {
			dialog.ShowInformation("Debug", "Debug logs are empty", w)
			return
		}

		dialog.ShowFileSave(func(writer fyne.URIWriteCloser, err error) {
			if err != nil {
				dialog.ShowError(err, s.Current)
				return
			}
			if writer == nil {
				return
			}

			saveDebugLogs(writer, s)
		}, s.Current)

	})

	gaplessText := widget.NewLabel("Gapless Playback")
	gaplessdropdown := widget.NewSelect([]string{"Enabled", "Disabled"}, func(ss string) {
		if ss == "Enabled" && fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled") == "Disabled" {
			dialog.ShowInformation("Gapless Playback", `Not all devices support gapless playback.
If 'Auto-Play Next File' is not working correctly, please disable it.`, w)
		}

		fyne.CurrentApp().Preferences().SetString("Gapless", ss)
		if s.NextMediaCheck.Checked {
			switch ss {
			case "Enabled":
				switch s.State {
				case "Playing", "Paused":
					newTVPayload, err := queueNext(s, false)
					if err == nil && s.GaplessMediaWatcher == nil {
						s.GaplessMediaWatcher = gaplessMediaWatcher
						go s.GaplessMediaWatcher(s.serverStopCTX, s, newTVPayload)
					}
				}
			case "Disabled":
				// We're disabling gapless playback. If for some reason
				// we fail to clear the NextURI it would be best to stop and
				// avoid inconsistencies where gapless playback appears disabled
				// but in reality it's not.
				_, err := queueNext(s, true)
				if err != nil {
					stopAction(s)
				}
			}
		}
	})
	gaplessOption := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")
	gaplessdropdown.SetSelected(gaplessOption)

	dropdown.Refresh()

	return container.New(layout.NewFormLayout(), themeText, dropdown, gaplessText, gaplessdropdown, ffmpegText, ffmpegTextEntry, debugText, debugExport)
}

func saveDebugLogs(f fyne.URIWriteCloser, s *NewScreen) {
	w := s.Current
	defer f.Close()

	s.Debug.ring.Do(func(p interface{}) {
		if p != nil {
			_, err := f.Write([]byte(p.(string)))
			if err != nil {
				dialog.ShowError(err, w)
			}
		}
	})
	dialog.ShowInformation("Debug", "Saved to... "+f.URI().String(), w)
}

func parseTheme(t string) {
	go func() {
		time.Sleep(10 * time.Millisecond)
		switch t {
		case "Light":
			fyne.CurrentApp().Preferences().SetString("Theme", "Light")
			fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Light"})
		case "Dark":
			fyne.CurrentApp().Preferences().SetString("Theme", "Dark")
			fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Dark"})
		}
	}()
}
