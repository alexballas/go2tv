//go:build !(android || ios)

package gui

import (
	"image/color"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
)

const playbackSurfaceRadius float32 = 8

// playbackSurface gives artwork a subtle neutral background.
type playbackSurface struct {
	widget.BaseWidget
	content fyne.CanvasObject
}

func newPlaybackSurface(content fyne.CanvasObject) *playbackSurface {
	s := &playbackSurface{content: content}
	s.ExtendBaseWidget(s)
	return s
}

func (s *playbackSurface) CreateRenderer() fyne.WidgetRenderer {
	background := canvas.NewRectangle(color.Transparent)
	background.CornerRadius = playbackSurfaceRadius
	r := &playbackSurfaceRenderer{
		WidgetRenderer: widget.NewSimpleRenderer(container.NewStack(background, s.content)),
		surface:        s, background: background,
	}
	r.Refresh()
	return r
}

type playbackSurfaceRenderer struct {
	fyne.WidgetRenderer
	surface    *playbackSurface
	background *canvas.Rectangle
}

func (r *playbackSurfaceRenderer) Refresh() {
	th := r.surface.Theme()
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	bg := color.NRGBAModel.Convert(th.Color(theme.ColorNameBackground, variant)).(color.NRGBA)
	fg := color.NRGBAModel.Convert(th.Color(theme.ColorNameForeground, variant)).(color.NRGBA)
	// Six percent foreground stays subtle in both light and dark themes.
	mix := func(b, f uint8) uint8 { return uint8((uint16(b)*94 + uint16(f)*6) / 100) }
	r.background.FillColor = color.NRGBA{R: mix(bg.R, fg.R), G: mix(bg.G, fg.G), B: mix(bg.B, fg.B), A: 255}
	r.WidgetRenderer.Refresh()
}
