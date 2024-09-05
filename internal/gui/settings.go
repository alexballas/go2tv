//go:build !(android || ios)
// +build !android,!ios

package gui

import (
	"runtime"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func settingsWindow(s *NewScreen) fyne.CanvasObject {
	w := s.Current

	themeText := widget.NewLabel(lang.L("Theme"))
	dropdownTheme := widget.NewSelect([]string{lang.L("Light"), lang.L("Dark")}, parseTheme)

	languageText := widget.NewLabel(lang.L("Language"))
	dropdownLanguage := widget.NewSelect([]string{"System Default", "English", "中文(简体)"}, parseLanguage(s))
	selectedLanguage := fyne.CurrentApp().Preferences().StringWithFallback("Language", "System Default")
	dropdownLanguage.PlaceHolder = selectedLanguage

	themeName := fyne.CurrentApp().Preferences().StringWithFallback("Theme", "GrabVariant")
	switch themeName {
	case "Light":
		dropdownTheme.PlaceHolder = lang.L("Light")
		parseTheme(lang.L("Light"))
	case "Dark":
		dropdownTheme.PlaceHolder = lang.L("Dark")
		parseTheme(lang.L("Dark"))
	case "GrabVariant", "Default":
		fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"GrabVariant"})

		// Wait for the SystemVariant variable to change
		<-signalSystemVariantChange

		switch SystemVariant {
		case theme.VariantDark:
			dropdownTheme.PlaceHolder = lang.L("Dark")
			parseTheme(lang.L("Dark"))
		case theme.VariantLight:
			dropdownTheme.PlaceHolder = lang.L("Light")
			parseTheme(lang.L("Light"))
		}
	}

	ffmpegText := widget.NewLabel("ffmpeg " + lang.L("Path"))
	ffmpegTextEntry := widget.NewEntry()

	ffmpegTextEntry.Text = func() string {
		if fyne.CurrentApp().Preferences().String("ffmpeg") != "" {
			return fyne.CurrentApp().Preferences().String("ffmpeg")
		}

		os := runtime.GOOS
		switch os {
		case "windows":
			return `C:\ffmpeg\bin\ffmpeg`
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
		s.ffmpegPathChanged = true
	}

	debugText := widget.NewLabel(lang.L("Debug"))
	debugExport := widget.NewButton(lang.L("Export Debug Logs"), func() {
		var itemInRing bool
		s.Debug.ring.Do(func(p interface{}) {
			if p != nil {
				itemInRing = true
			}
		})

		if !itemInRing {
			dialog.ShowInformation(lang.L("Debug"), lang.L("Debug logs are empty"), w)
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

	gaplessText := widget.NewLabel(lang.L("Gapless Playback"))
	gaplessdropdown := widget.NewSelect([]string{"Enabled", "Disabled"}, func(ss string) {
		if ss == "Enabled" && fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled") == "Disabled" {
			dialog.ShowInformation(lang.L("Gapless Playback"), lang.L(`Not all devices support gapless playback. If 'Auto-Play Next File' is not working correctly, please disable it.`), w)
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

	dropdownTheme.Refresh()

	return container.New(layout.NewFormLayout(), themeText, dropdownTheme, languageText, dropdownLanguage, gaplessText, gaplessdropdown, ffmpegText, ffmpegTextEntry, debugText, debugExport)
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
	dialog.ShowInformation(lang.L("Debug"), lang.L("Saved to")+"... "+f.URI().String(), w)
}

func parseTheme(t string) {
	go func() {
		time.Sleep(10 * time.Millisecond)
		switch t {
		case lang.L("Light"):
			fyne.CurrentApp().Preferences().SetString("Theme", "Light")
			fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Light"})
		case lang.L("Dark"):
			fyne.CurrentApp().Preferences().SetString("Theme", "Dark")
			fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Dark"})
		}
	}()
}

func parseLanguage(s *NewScreen) func(string) {
	w := s.Current
	return func(t string) {
		if t != fyne.CurrentApp().Preferences().StringWithFallback("Language", "System Default") {
			dialog.ShowInformation(lang.L("Update Language Preferences"), lang.L(`Please restart the application for the changes to take effect.`), w)
		}
		go func() {
			switch t {
			case "English":
				fyne.CurrentApp().Preferences().SetString("Language", "English")
			case "中文(简体)":
				fyne.CurrentApp().Preferences().SetString("Language", "中文(简体)")
			default:
				fyne.CurrentApp().Preferences().SetString("Language", "System Default")
			}
		}()
	}
}
