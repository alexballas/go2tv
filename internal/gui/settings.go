//go:build !(android || ios)
// +build !android,!ios

package gui

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type go2tvTheme struct {
	Theme string
}

var _ fyne.Theme = go2tvTheme{}

func (m go2tvTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch m.Theme {
	case "Dark":
		variant = theme.VariantDark
		switch name {
		case theme.ColorNameDisabled:
			return color.NRGBA{R: 0x55, G: 0x55, B: 0x55, A: 0xff}
		case theme.ColorNameBackground:
			return color.NRGBA{R: 0x30, G: 0x30, B: 0x30, A: 0xff}
		case theme.ColorNameButton:
			return color.NRGBA{R: 0x44, G: 0x44, B: 0x44, A: 0xff}
		case theme.ColorNameDisabledButton:
			return color.NRGBA{R: 0x26, G: 0x26, B: 0x26, A: 0xff}
		case theme.ColorNameOverlayBackground:
			return color.NRGBA{R: 0x30, G: 0x30, B: 0x30, A: 0xff}
		case theme.ColorNameMenuBackground:
			return color.NRGBA{R: 0x30, G: 0x30, B: 0x30, A: 0xff}
		}

	case "Light":
		variant = theme.VariantLight
		switch name {
		case theme.ColorNameDisabled:
			return color.NRGBA{R: 0xab, G: 0xab, B: 0xab, A: 0xff}
		case theme.ColorNameInputBorder:
			return color.NRGBA{R: 0xf3, G: 0xf3, B: 0xf3, A: 0xff}
		case theme.ColorNameDisabledButton:
			return color.NRGBA{R: 0xe5, G: 0xe5, B: 0xe5, A: 0xff}
		}
	}
	theme.InnerPadding()
	return theme.DefaultTheme().Color(name, variant)
}

func (m go2tvTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (m go2tvTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (m go2tvTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

func settingsWindow(s *NewScreen) fyne.CanvasObject {
	w := s.Current

	themeText := widget.NewLabel("Theme")
	dropdown := widget.NewSelect([]string{"Light", "Dark", "Default"}, parseTheme(s))
	theme := fyne.CurrentApp().Preferences().StringWithFallback("Theme", "Default")
	switch theme {
	case "Light":
		dropdown.PlaceHolder = "Light"
	case "Dark":
		dropdown.PlaceHolder = "Dark"
	case "Default":
		dropdown.PlaceHolder = "Default"
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

	return container.New(layout.NewFormLayout(), themeText, dropdown, gaplessText, gaplessdropdown, debugText, debugExport)
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

func parseTheme(s *NewScreen) func(string) {
	return func(t string) {
		go func() {
			time.Sleep(10 * time.Millisecond)
			switch t {
			case "Light":
				fyne.CurrentApp().Preferences().SetString("Theme", "Light")
				fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Light"})
			case "Dark":
				fyne.CurrentApp().Preferences().SetString("Theme", "Dark")
				fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Dark"})
			case "Default":
				fyne.CurrentApp().Preferences().SetString("Theme", "Default")
				fyne.CurrentApp().Settings().SetTheme(go2tvTheme{"Default"})
			}
		}()
	}
}
