package gui

import (
	"image/color"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type go2tvTheme struct {
	Theme string
}

func (m go2tvTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch m.Theme {
	case "Dark":
		variant = theme.VariantDark
	case "Light":
		variant = theme.VariantLight
	}

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
	themeText := canvas.NewText("Theme", nil)
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

	debugText := canvas.NewText("Debug", nil)
	debugExport := widget.NewButton("Export Debug Logs", func() {
		dialog.ShowFileSave(func(writer fyne.URIWriteCloser, err error) {
			if err != nil {
				dialog.ShowError(err, s.Current)
				return
			}
			if writer == nil {
				log.Println("Cancelled")
				return
			}

			saveDebugLogs(writer, s)
		}, s.Current)

	})

	dropdown.Refresh()
	settings := container.New(layout.NewFormLayout(), themeText, dropdown, debugText, debugExport)
	return settings
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
	dialog.ShowInformation("Debug File", "Saved to... "+f.URI().String(), w)
}

func parseTheme(s *NewScreen) func(string) {
	return func(t string) {
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
	}
}
