//go:build !(android || ios)

package gui

import (
	"fmt"
	"image/color"
	"strings"

	ttwidget "github.com/alexballas/fyne-tooltip/widget"
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/driver/desktop"
	"github.com/alexballas/refyne/v2/theme"
)

// playbackToggle keeps Check's state and callbacks, with a compact switch surface.
type playbackToggle struct {
	ttwidget.Check
	icon             fyne.Resource
	focused, hovered bool
}

func newPlaybackToggle(text string, icon fyne.Resource) *playbackToggle {
	t := &playbackToggle{icon: icon}
	t.Text = text
	t.ExtendBaseWidget(t)
	return t
}

func (t *playbackToggle) MinSize() fyne.Size { return t.BaseWidget.MinSize() }

func (t *playbackToggle) Tapped(*fyne.PointEvent) {
	if t.Disabled() {
		return
	}
	if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil {
		c.Focus(t)
	}
	t.SetChecked(!t.Checked)
}

func (t *playbackToggle) FocusGained() {
	if t.Disabled() {
		return
	}
	t.focused = true
	t.Check.FocusGained()
}
func (t *playbackToggle) FocusLost() { t.focused = false; t.Check.FocusLost() }
func (t *playbackToggle) MouseIn(e *desktop.MouseEvent) {
	t.Check.ToolTipWidgetExtend.MouseIn(e)
	t.hovered = true
	t.Refresh()
}
func (t *playbackToggle) MouseOut() {
	t.Check.ToolTipWidgetExtend.MouseOut()
	t.hovered = false
	t.Refresh()
}
func (t *playbackToggle) MouseMoved(e *desktop.MouseEvent) { t.Check.ToolTipWidgetExtend.MouseMoved(e) }

func (t *playbackToggle) CreateRenderer() fyne.WidgetRenderer {
	background := canvas.NewRectangle(color.Transparent)
	background.CornerRadius = 10
	background.StrokeWidth = 1
	icon := canvas.NewImageFromResource(t.icon)
	icon.FillMode = canvas.ImageFillContain
	label := canvas.NewText(t.Text, color.Transparent)
	track := canvas.NewRectangle(color.Transparent)
	track.CornerRadius = 11
	thumb := canvas.NewCircle(color.White)
	r := &playbackToggleRenderer{toggle: t, background: background, icon: icon, label: label, track: track, thumb: thumb}
	r.Refresh()
	return r
}

type playbackToggleRenderer struct {
	toggle     *playbackToggle
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
	if r.toggle.Checked {
		x += 14
	}
	r.thumb.Move(fyne.NewPos(x+3, y+3))
	r.thumb.Resize(fyne.NewSize(14, 14))
}
func (r *playbackToggleRenderer) Refresh() {
	t := r.toggle
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
	if t.hovered && !t.Disabled() {
		r.background.FillColor = th.Color(theme.ColorNameHover, v)
	}
	if t.focused && !t.Disabled() {
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
	svg := strings.ReplaceAll(string(t.icon.Content()), "#000", "#"+hex)
	// Fyne caches images by resource name; each tint needs its own identity.
	name := strings.TrimSuffix(t.icon.Name(), ".svg") + "-" + hex + ".svg"
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

func playbackAutoplayIcon() fyne.Resource {
	return fyne.NewStaticResource("playback-autoplay.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#000" d="M3 4a1 1 0 0 1 1.6-.8l9 6.8a1 1 0 0 1 0 1.6l-9 6.8A1 1 0 0 1 3 17.6V4zM16 3h2v11h-2z"/><path fill="#000" d="M18 15l5 4-5 4v-3h-7v-2h7v-3z"/></svg>`))
}

func playbackTranscodeIcon() fyne.Resource {
	return fyne.NewStaticResource("playback-transcode.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#000" d="M9 1h4l.6 3 2 1.2 2.8-1 2 3.5-2.3 2 .1 2.3-3 .6a4.7 4.7 0 1 0-5.5 3.7l-.7 3-1.5-.6-2.8 1-2-3.5 2.3-2V10L1.8 8l2-3.5 2.8 1 2-1.2L9 1zM20 12l4 4h-3a5 5 0 0 0-8 1l-2-1a7 7 0 0 1 9-3v-1zM12 24l-4-4h3a5 5 0 0 0 8-1l2 1a7 7 0 0 1-9 3v1z"/></svg>`))
}

func playbackDesktopIcon() fyne.Resource {
	return fyne.NewStaticResource("playback-desktop.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#000" fill-rule="evenodd" d="M3 3h18a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-7v2h4v2H6v-2h4v-2H3a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2zm0 2v12h18V5H3z"/><path fill="#000" d="M10 7l6 4-6 4V7z"/></svg>`))
}

func playbackServerIcon() fyne.Resource {
	return fyne.NewStaticResource("playback-server.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#000" fill-rule="evenodd" d="M4 2h16a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2zm0 2v5h16V4H4zm0 9h16a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-5a2 2 0 0 1 2-2zm0 2v5h16v-5H4z"/><path fill="#000" d="M6 5h2v3H6zm4 1h8v1h-8zM6 16h2v3H6zm4 1h8v1h-8z"/></svg>`))
}
