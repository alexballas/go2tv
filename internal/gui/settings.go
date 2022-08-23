package gui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
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
	debug := widget.NewButton("Open Debug Window", func() {
		s.Debug.enabled = true
		if s.Debug.bb.Len() > 0 {
			s.Debug.entry.ParseMarkdown("")
		}

		if s.Debug.window != nil {
			s.Debug.window.Close()
			s.Debug.window = fyne.CurrentApp().NewWindow("Debug Window")
		}
		s.Debug.entry.Wrapping = fyne.TextWrap(3)
		s.Debug.window.SetContent(container.NewScroll((s.Debug.entry)))
		s.Debug.window.Resize(fyne.NewSize(800, 600))
		s.Debug.window.CenterOnScreen()
		s.Debug.window.SetOnClosed(func() {
			s.Debug.enabled = false
		})
		s.Debug.window.Show()
	})

	dropdown.Refresh()
	settings := container.New(layout.NewFormLayout(), themeText, dropdown, debugText, debug)
	return settings
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
