package gui

import (
	"image/color"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/theme"
)

type go2tvTheme struct {
	Theme string
}

var _ fyne.Theme = go2tvTheme{}

func (m go2tvTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch {
	case m.Theme == "Dark" || (m.Theme == "System Default" && variant == theme.VariantDark):
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
		case theme.ColorNameSeparator:
			return color.NRGBA{R: 0x26, G: 0x26, B: 0x26, A: 0xff}
		case theme.ColorNameOverlayBackground:
			return color.NRGBA{R: 0x30, G: 0x30, B: 0x30, A: 0xff}
		case theme.ColorNameMenuBackground:
			return color.NRGBA{R: 0x30, G: 0x30, B: 0x30, A: 0xff}
		}

	case m.Theme == "Light" || (m.Theme == "System Default" && variant == theme.VariantLight):
		variant = theme.VariantLight
		switch name {
		case theme.ColorNameDisabled:
			return color.NRGBA{R: 0xab, G: 0xab, B: 0xab, A: 0xff}
		case theme.ColorNameInputBorder:
			return color.NRGBA{R: 0xd0, G: 0xd0, B: 0xd0, A: 0xff}
		case theme.ColorNameInputBackground:
			return color.NRGBA{R: 0xe8, G: 0xe8, B: 0xe8, A: 0xff}
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
	if size, ok := platformThemeSize(name); ok {
		return size
	}
	return theme.DefaultTheme().Size(name)
}

// Material "cast" icon on the 24px grid, matching padding of theme icons.
var castIconSVG = []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#000000" d="M21 3H3c-1.1 0-2 .9-2 2v3h2V5h18v14h-7v2h7c1.1 0 2-.9 2-2V5c0-1.1-.9-2-2-2zM1 18v3h3c0-1.66-1.34-3-3-3zm0-4v2c2.76 0 5 2.24 5 5h2c0-3.87-3.13-7-7-7zm0-4v2c4.97 0 9 4.03 9 9h2c0-6.08-4.93-11-11-11z"/></svg>`)

// Material "play_arrow" shifted left so its edge lines up with castIcon in device rows.
var playingIconSVG = []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#000000" d="M2 5v14l11-7z"/></svg>`)

func playingIcon() fyne.Resource {
	return theme.NewThemedResource(
		fyne.NewStaticResource("playing.svg", playingIconSVG),
	)
}

func castIcon() fyne.Resource {
	return theme.NewThemedResource(
		fyne.NewStaticResource("cast.svg", castIconSVG),
	)
}
