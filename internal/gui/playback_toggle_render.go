package gui

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
)

// toggleLook is the per-platform toggle state the shared renderer reads.
type toggleLook struct {
	icon             fyne.Resource
	focused, hovered bool
}

// newPlaybackToggleRenderer draws a Check as icon, label and switch.
func newPlaybackToggleRenderer(check *widget.Check, look *toggleLook) fyne.WidgetRenderer {
	background := canvas.NewRectangle(color.Transparent)
	background.CornerRadius = 10
	icon := canvas.NewImageFromResource(look.icon)
	icon.FillMode = canvas.ImageFillContain
	label := canvas.NewText(check.Text, color.Transparent)
	track := canvas.NewRectangle(color.Transparent)
	track.CornerRadius = 11
	thumb := canvas.NewCircle(color.White)
	r := &playbackToggleRenderer{check: check, look: look, background: background, icon: icon, label: label, track: track, thumb: thumb}
	r.Refresh()
	return r
}

type playbackToggleRenderer struct {
	check      *widget.Check
	look       *toggleLook
	background *canvas.Rectangle
	icon       *canvas.Image
	label      *canvas.Text
	track      *canvas.Rectangle
	thumb      *canvas.Circle
}

func (r *playbackToggleRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.background, r.icon, r.label, r.track, r.thumb}
}
func (r *playbackToggleRenderer) Destroy() {}
func (r *playbackToggleRenderer) MinSize() fyne.Size {
	return fyne.NewSize(r.label.MinSize().Width+92, fyne.Max(44, r.label.MinSize().Height+20))
}
func (r *playbackToggleRenderer) Layout(size fyne.Size) {
	r.background.Resize(size)
	r.icon.Move(fyne.NewPos(10, (size.Height-20)/2))
	r.icon.Resize(fyne.NewSize(20, 20))
	r.label.Move(fyne.NewPos(38, (size.Height-r.label.MinSize().Height)/2))
	r.label.Resize(r.label.MinSize())
	x, y := fyne.Min(size.Width-44, r.label.MinSize().Width+50), (size.Height-20)/2
	r.track.Move(fyne.NewPos(x, y))
	r.track.Resize(fyne.NewSize(34, 20))
	if r.check.Checked {
		x += 14
	}
	r.thumb.Move(fyne.NewPos(x+3, y+3))
	r.thumb.Resize(fyne.NewSize(14, 14))
}
func (r *playbackToggleRenderer) Refresh() {
	t, look := r.check, r.look
	th, v := t.Theme(), fyne.CurrentApp().Settings().ThemeVariant()
	accent := color.NRGBA{R: 0x0d, G: 0xb5, B: 0x70, A: 0xff}
	foreground := th.Color(theme.ColorNameForeground, v)
	iconColor := th.Color(theme.ColorNamePlaceHolder, v)
	r.background.FillColor = color.Transparent
	r.background.StrokeColor = color.Transparent
	r.background.StrokeWidth = 0
	r.track.FillColor = th.Color(theme.ColorNamePlaceHolder, v)
	r.thumb.FillColor = color.White
	if t.Checked {
		iconColor = accent
		r.track.FillColor = accent
	}
	if look.hovered && !t.Disabled() {
		r.background.FillColor = th.Color(theme.ColorNameHover, v)
	}
	if look.focused && !t.Disabled() {
		r.background.StrokeColor = accent
		r.background.StrokeWidth = 2
	}
	if t.Disabled() {
		foreground = th.Color(theme.ColorNameDisabled, v)
		iconColor = th.Color(theme.ColorNameDisabled, v)
		r.track.FillColor = th.Color(theme.ColorNameDisabledButton, v)
		r.thumb.FillColor = th.Color(theme.ColorNameDisabled, v)
	}
	ink := color.NRGBAModel.Convert(iconColor).(color.NRGBA)
	hex := fmt.Sprintf("%02x%02x%02x", ink.R, ink.G, ink.B)
	svg := strings.ReplaceAll(string(look.icon.Content()), "#000", "#"+hex)
	// Fyne caches images by resource name; each tint needs its own identity.
	name := strings.TrimSuffix(look.icon.Name(), ".svg") + "-" + hex + ".svg"
	r.icon.Resource = fyne.NewStaticResource(name, []byte(svg))
	r.label.Text = t.Text
	r.label.TextSize = th.Size(theme.SizeNameText)
	r.label.Color = foreground
	r.Layout(t.Size())
	for _, object := range r.Objects() {
		object.Refresh()
	}
}

func playbackLoopIcon() fyne.Resource {
	return fyne.NewStaticResource("playback-loop.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#000" d="M16 2l6 5-6 5V8H7a3 3 0 0 0-3 3v1H1v-1a6 6 0 0 1 6-6h9V2zM8 22l-6-5 6-5v4h9a3 3 0 0 0 3-3v-1h3v1a6 6 0 0 1-6 6H8v3z"/></svg>`))
}

func playbackTranscodeIcon() fyne.Resource {
	return fyne.NewStaticResource("playback-transcode.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#000" d="M9 1h4l.6 3 2 1.2 2.8-1 2 3.5-2.3 2 .1 2.3-3 .6a4.7 4.7 0 1 0-5.5 3.7l-.7 3-1.5-.6-2.8 1-2-3.5 2.3-2V10L1.8 8l2-3.5 2.8 1 2-1.2L9 1zM20 12l4 4h-3a5 5 0 0 0-8 1l-2-1a7 7 0 0 1 9-3v-1zM12 24l-4-4h3a5 5 0 0 0 8-1l2 1a7 7 0 0 1-9 3v1z"/></svg>`))
}
