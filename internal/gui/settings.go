//go:build !(android || ios)
// +build !android,!ios

package gui

import (
	"os/exec"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func settingsWindow(s *FyneScreen) fyne.CanvasObject {
	w := s.Current

	themeText := widget.NewLabel(lang.L("Theme"))
	dropdownTheme := widget.NewSelect([]string{lang.L("System Default"), lang.L("Light"), lang.L("Dark")}, parseTheme)

	languageText := widget.NewLabel(lang.L("Language"))
	dropdownLanguage := widget.NewSelect([]string{lang.L("System Default"), "English", "中文(简体)"}, parseLanguage(s))
	selectedLanguage := fyne.CurrentApp().Preferences().StringWithFallback("Language", "System Default")

	if selectedLanguage == "System Default" {
		selectedLanguage = lang.L("System Default")
	}

	dropdownLanguage.PlaceHolder = selectedLanguage

	themeName := lang.L(fyne.CurrentApp().Preferences().StringWithFallback("Theme", "System Default"))
	dropdownTheme.PlaceHolder = themeName
	parseTheme(themeName)

	s.systemTheme = fyne.CurrentApp().Settings().ThemeVariant()

	ffmpegText := widget.NewLabel("ffmpeg " + lang.L("Path"))
	ffmpegTextEntry := widget.NewEntry()

	ffmpegFolderReset := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		path, err := exec.LookPath("ffmpeg")
		ffmpegTextEntry.SetText(path)
		if err != nil {
			ffmpegTextEntry.SetText("ffmpeg")
		}
		s.ffmpegPath = ffmpegTextEntry.Text
	})

	ffmpegFolderSelect := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
		fd := dialog.NewFolderOpen(func(lu fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if lu == nil {
				return
			}

			p := filepath.ToSlash(lu.Path() + string(filepath.Separator) + "ffmpeg")
			ffmpegTextEntry.SetText(p)
		}, w)

		fd.Resize(fyne.NewSize(w.Canvas().Size().Width*1.2, w.Canvas().Size().Height*1.3))
		fd.Show()

	})

	ffmpegRightButtons := container.NewHBox(ffmpegFolderSelect, ffmpegFolderReset)
	ffmpegPathControls := container.New(layout.NewBorderLayout(nil, nil, nil, ffmpegRightButtons), ffmpegRightButtons, ffmpegTextEntry)

	ffmpegTextEntry.Text = func() string {
		if fyne.CurrentApp().Preferences().String("ffmpeg") != "" {
			return fyne.CurrentApp().Preferences().String("ffmpeg")
		}

		path, _ := exec.LookPath("ffmpeg")
		return path

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

		fd := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
			if err != nil {
				dialog.ShowError(err, s.Current)
				return
			}
			if writer == nil {
				return
			}

			saveDebugLogs(writer, s)
		}, s.Current)

		fd.Resize(fyne.NewSize(w.Canvas().Size().Width*1.2, w.Canvas().Size().Height*1.3))
		fd.Show()
	})

	gaplessText := widget.NewLabel(lang.L("Gapless Playback"))
	gaplessdropdown := widget.NewSelect([]string{lang.L("Enabled"), lang.L("Disabled")}, func(ss string) {
		var selection string
		if lang.L("Enabled") == ss {
			selection = "Enabled"
		}

		if lang.L("Disabled") == ss {
			selection = "Disabled"
		}

		if selection == "Enabled" && fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled") == "Disabled" {
			dialog.ShowInformation(lang.L("Gapless Playback"), lang.L(`Some devices don't support gapless playback. If 'Auto-Play Next File' isn't working properly, try turning it off.`), w)
		}

		fyne.CurrentApp().Preferences().SetString("Gapless", selection)
		if s.NextMediaCheck.Checked {
			switch selection {
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
	gaplessdropdown.SetSelected(lang.L(gaplessOption))

	dropdownTheme.Refresh()

	return container.New(layout.NewFormLayout(), themeText, dropdownTheme, languageText, dropdownLanguage, gaplessText, gaplessdropdown, ffmpegText, ffmpegPathControls, debugText, debugExport)
}

func saveDebugLogs(f fyne.URIWriteCloser, s *FyneScreen) {
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
			fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Light"})
			fyne.CurrentApp().Preferences().SetString("Theme", "Light")
		case lang.L("Dark"):
			fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Dark"})
			fyne.CurrentApp().Preferences().SetString("Theme", "Dark")
		default:
			fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"System Default"})
			fyne.CurrentApp().Preferences().SetString("Theme", "System Default")
		}
	}()
}

func parseLanguage(s *FyneScreen) func(string) {
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
