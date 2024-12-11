package gui

import (
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

type go2tvTheme struct {
	Theme string
}

var (
	_                         fyne.Theme        = go2tvTheme{}
	systemVariant             fyne.ThemeVariant = 999
	signalSystemVariantChange                   = make(chan struct{})
	once                      sync.Once
)

func (m go2tvTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch m.Theme {
	case "GrabVariant":
		once.Do(func() {
			systemVariant = variant
			go func() {
				signalSystemVariantChange <- struct{}{}
			}()
		})

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
