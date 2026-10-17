//go:build android || ios

package gui

import (
	"image/color"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/layout"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
)

// spreadRowLayout spreads items edge to edge with equal gaps when they fit on
// one row, and falls back to row wrapping when the screen is too narrow.
type spreadRowLayout struct {
	wrap fyne.Layout
}

func newSpreadRowLayout() *spreadRowLayout {
	return &spreadRowLayout{wrap: layout.NewRowWrapLayout()}
}

func (l *spreadRowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return l.wrap.MinSize(objects)
}

func (l *spreadRowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	// Always wrap first: it also caches the min size the parent relies on.
	l.wrap.Layout(objects, size)

	var visible []fyne.CanvasObject
	var used, h float32
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		visible = append(visible, o)
		used += o.MinSize().Width
		h = max(h, o.MinSize().Height)
	}
	if len(visible) < 2 || l.wrap.MinSize(objects).Height > h {
		return
	}

	gap := (size.Width - used) / float32(len(visible)-1)
	var x float32
	for _, o := range visible {
		o.Move(fyne.NewPos(x, 0))
		x += o.MinSize().Width + gap
	}
}

// playerButtonsLayout gives Play a pill filling the free width, followed by
// round icon buttons. The player bar theme turns square buttons into circles.
type playerButtonsLayout struct{}

const playerButtonMinHeight = 52

func (*playerButtonsLayout) height(objects []fyne.CanvasObject) float32 {
	h := float32(playerButtonMinHeight)
	for _, o := range objects {
		h = max(h, o.MinSize().Height)
	}
	return h
}

func (l *playerButtonsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	h := l.height(objects)
	gap := theme.Padding()
	w := objects[0].MinSize().Width
	for range objects[1:] {
		w += gap + h
	}
	return fyne.NewSize(w, h)
}

func (l *playerButtonsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	h := l.height(objects)
	gap := theme.Padding()
	round := len(objects) - 1
	play := max(objects[0].MinSize().Width, size.Width-float32(round)*(h+gap))
	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(fyne.NewSize(play, h))
	x := play + gap
	for _, o := range objects[1:] {
		o.Move(fyne.NewPos(x, 0))
		o.Resize(fyne.NewSquareSize(h))
		x += h + gap
	}
}

// playerBar is the rounded card holding the timeline and player buttons.
type playerBar struct {
	widget.BaseWidget
	content fyne.CanvasObject
}

const (
	playerBarRadius = 24
	pillRadius      = 999 // Clamped to half the height by the painter.
)

func newPlayerBar(content fyne.CanvasObject) fyne.CanvasObject {
	bar := &playerBar{content: content}
	bar.ExtendBaseWidget(bar)
	return container.NewThemeOverride(bar, pillTheme)
}

func (b *playerBar) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(nil)
	bg.CornerRadius = playerBarRadius
	r := &playerBarRenderer{bar: b, bg: bg}
	r.Refresh()
	return r
}

type playerBarRenderer struct {
	bar *playerBar
	bg  *canvas.Rectangle
}

func (r *playerBarRenderer) inset() float32 { return theme.InnerPadding() }

func (r *playerBarRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	in := r.inset()
	r.bar.content.Move(fyne.NewPos(in, in))
	r.bar.content.Resize(size.SubtractWidthHeight(2*in, 2*in))
}

func (r *playerBarRenderer) MinSize() fyne.Size {
	return r.bar.content.MinSize().AddWidthHeight(2*r.inset(), 2*r.inset())
}

func (r *playerBarRenderer) Refresh() {
	th := r.bar.Theme()
	r.bg.FillColor = th.Color(theme.ColorNameInputBackground, fyne.CurrentApp().Settings().ThemeVariant())
	r.bg.Refresh()
	r.bar.content.Refresh()
}

func (r *playerBarRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.bar.content}
}

func (*playerBarRenderer) Destroy() {}

// liveTheme follows the app theme live, remapping selected colors and sizes.
type liveTheme struct {
	colors map[fyne.ThemeColorName]fyne.ThemeColorName
	sizes  map[fyne.ThemeSizeName]float32
}

// pillTheme rounds buttons into pills/circles. Disabled buttons keep the
// regular button fill so their shape stays visible on the input-colored card,
// with clearly dimmed content.
var pillTheme = playerTheme{liveTheme{
	colors: map[fyne.ThemeColorName]fyne.ThemeColorName{theme.ColorNameDisabledButton: theme.ColorNameButton},
	sizes:  map[fyne.ThemeSizeName]float32{theme.SizeNameInputRadius: pillRadius},
}}

type playerTheme struct{ liveTheme }

const disabledContentAlpha = 0x66

func (t playerTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if n == theme.ColorNameDisabled {
		r, g, b, _ := t.current().Color(theme.ColorNameForeground, v).RGBA()
		return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: disabledContentAlpha}
	}
	return t.liveTheme.Color(n, v)
}

func (liveTheme) current() fyne.Theme { return fyne.CurrentApp().Settings().Theme() }

func (t liveTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if alias, ok := t.colors[n]; ok {
		n = alias
	}
	return t.current().Color(n, v)
}

func (t liveTheme) Font(s fyne.TextStyle) fyne.Resource { return t.current().Font(s) }

func (t liveTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return t.current().Icon(n) }

func (t liveTheme) Size(n fyne.ThemeSizeName) float32 {
	if size, ok := t.sizes[n]; ok {
		return size
	}
	return t.current().Size(n)
}

// valueField shows a read-only value in a rounded field. Long names truncate
// with an ellipsis, an empty value shows a dim placeholder, and tapping runs
// OnTapped (e.g. a picker).
type valueField struct {
	widget.BaseWidget
	label    *widget.Label
	OnTapped func()
}

func newValueField(onTapped func()) *valueField {
	label := widget.NewLabel("")
	label.Truncation = fyne.TextTruncateEllipsis
	f := &valueField{label: label, OnTapped: onTapped}
	f.ExtendBaseWidget(f)
	return f
}

// Set shows value, or placeholder at low importance when value is empty.
func (f *valueField) Set(value, placeholder string) {
	f.label.Importance = widget.MediumImportance
	if value == "" {
		value = placeholder
		f.label.Importance = widget.LowImportance
	}
	f.label.SetText(value)
}

func (f *valueField) Tapped(*fyne.PointEvent) {
	if f.OnTapped != nil {
		f.OnTapped()
	}
}

func (f *valueField) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(nil)
	bg.StrokeWidth = 1
	r := &valueFieldRenderer{field: f, bg: bg}
	r.Refresh()
	return r
}

type valueFieldRenderer struct {
	field *valueField
	bg    *canvas.Rectangle
}

func (r *valueFieldRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.field.label.Resize(size)
}

func (r *valueFieldRenderer) MinSize() fyne.Size {
	// Match an Entry's height so swapping with the URL entry keeps the row.
	return fyne.NewSize(r.field.label.MinSize().Width, widget.NewEntry().MinSize().Height)
}

func (r *valueFieldRenderer) Refresh() {
	th := r.field.Theme()
	v := fyne.CurrentApp().Settings().ThemeVariant()
	r.bg.FillColor = th.Color(theme.ColorNameInputBackground, v)
	r.bg.StrokeColor = th.Color(theme.ColorNameInputBorder, v)
	r.bg.CornerRadius = th.Size(theme.SizeNameInputRadius)
	r.bg.Refresh()
	r.field.label.Refresh()
}

func (r *valueFieldRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.field.label}
}

func (*valueFieldRenderer) Destroy() {}

// mobileMainLayout orders source, devices, player. Portrait stacks them with
// the player pinned to the bottom and the device list taking the slack.
// Landscape puts devices in a full-height right column.
type mobileMainLayout struct {
	source *container.Scroll
}

// Portrait share of free height kept for devices before the source scrolls.
const mobileDeviceMinShare = 0.35

func (l *mobileMainLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	devices, player := objects[1], objects[2]
	width := max(l.source.Content.MinSize().Width, devices.MinSize().Width, player.MinSize().Width)
	// The source scrolls, so it never forces the screen taller.
	return fyne.NewSize(width, devices.MinSize().Height+player.MinSize().Height+2*theme.Padding())
}

func (l *mobileMainLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	source, devices, player := objects[0], objects[1], objects[2]
	gap := theme.Padding()
	playerHeight := player.MinSize().Height

	if size.Width > size.Height {
		left := (size.Width - gap) * 0.55
		source.Move(fyne.NewPos(0, 0))
		source.Resize(fyne.NewSize(left, max(0, size.Height-playerHeight-gap)))
		player.Move(fyne.NewPos(0, size.Height-playerHeight))
		player.Resize(fyne.NewSize(left, playerHeight))
		devices.Move(fyne.NewPos(left+gap, 0))
		devices.Resize(fyne.NewSize(size.Width-left-gap, size.Height))
		return
	}

	free := max(0, size.Height-playerHeight-2*gap)
	sourceHeight := min(l.source.Content.MinSize().Height, free*(1-mobileDeviceMinShare))
	source.Move(fyne.NewPos(0, 0))
	source.Resize(fyne.NewSize(size.Width, sourceHeight))
	// Wrapping rows only learn their height once laid out at this width; the
	// first pass can see them fully wrapped. Settle on the measured height.
	if settled := min(l.source.Content.MinSize().Height, free*(1-mobileDeviceMinShare)); settled != sourceHeight {
		sourceHeight = settled
		source.Resize(fyne.NewSize(size.Width, sourceHeight))
	}
	devices.Move(fyne.NewPos(0, sourceHeight+gap))
	devices.Resize(fyne.NewSize(size.Width, free-sourceHeight))
	player.Move(fyne.NewPos(0, size.Height-playerHeight))
	player.Resize(fyne.NewSize(size.Width, playerHeight))
}

// contentSized reports its scroll content's size as MinSize. Scroll MinSize
// never changes, so without this the parent layout would not rerun when the
// content grows, e.g. when the radio "Now playing" line appears.
type contentSized struct {
	widget.BaseWidget
	scroll *container.Scroll
}

func newContentSized(scroll *container.Scroll) *contentSized {
	c := &contentSized{scroll: scroll}
	c.ExtendBaseWidget(c)
	return c
}

func (c *contentSized) MinSize() fyne.Size { return c.scroll.Content.MinSize() }

func (c *contentSized) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(c.scroll)
}
