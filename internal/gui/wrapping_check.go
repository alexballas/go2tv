package gui

import (
	ttwidget "github.com/alexballas/fyne-tooltip/widget"
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/driver/desktop"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
)

// Keep the native check's input, focus, tooltip and state handling, replacing
// only its single-line caption with a label that can wrap.
type wrappingCheck struct {
	ttwidget.Check
}

func newWrappingCheck(text string, changed func(bool)) *wrappingCheck {
	c := &wrappingCheck{Check: ttwidget.Check{Check: widget.Check{Text: text, OnChanged: changed}}}
	c.ExtendBaseWidget(c)
	return c
}

// The native check limits input to its minimum width. A wrapped caption fills
// the allocated width, so forward input from the entire row to its active area.
func (c *wrappingCheck) Tapped(_ *fyne.PointEvent) {
	c.Check.Tapped(&fyne.PointEvent{Position: fyne.NewPos(0, c.Size().Height/2)})
}

func (c *wrappingCheck) MouseIn(event *desktop.MouseEvent) {
	c.ToolTipWidgetExtend.MouseIn(event)
	c.MouseMoved(event)
}

func (c *wrappingCheck) MouseMoved(event *desktop.MouseEvent) {
	c.ToolTipWidgetExtend.MouseMoved(event)
	active := *event
	active.Position = fyne.NewPos(0, c.Size().Height/2)
	c.Check.Check.MouseMoved(&active)
}

func (c *wrappingCheck) CreateRenderer() fyne.WidgetRenderer {
	native := c.Check.CreateRenderer()
	label := widget.NewLabel(c.Text)
	label.Wrapping = fyne.TextWrapWord
	objects := make([]fyne.CanvasObject, 0, len(native.Objects()))
	for _, object := range native.Objects() {
		if _, caption := object.(*canvas.Text); !caption {
			objects = append(objects, object)
		}
	}
	objects = append(objects, label)
	r := &wrappingCheckRenderer{WidgetRenderer: native, check: c, label: label, objects: objects}
	r.Refresh()
	return r
}

type wrappingCheckRenderer struct {
	fyne.WidgetRenderer
	check   *wrappingCheck
	label   *widget.Label
	objects []fyne.CanvasObject
}

func (r *wrappingCheckRenderer) captionOffset() float32 {
	th := r.check.Theme()
	return th.Size(theme.SizeNameInlineIcon) + 2*th.Size(theme.SizeNameInputBorder)
}

func (r *wrappingCheckRenderer) Layout(size fyne.Size) {
	r.WidgetRenderer.Layout(size)
	x := r.captionOffset()
	r.label.Resize(fyne.NewSize(max(0, size.Width-x), size.Height))
	height := r.label.MinSize().Height
	r.label.Resize(fyne.NewSize(max(0, size.Width-x), height))
	r.label.Move(fyne.NewPos(x, (size.Height-height)/2))
}

func (r *wrappingCheckRenderer) MinSize() fyne.Size {
	th := r.check.Theme()
	label := r.label.MinSize()
	return fyne.NewSize(r.captionOffset()+label.Width,
		max(label.Height, th.Size(theme.SizeNameInlineIcon)+2*th.Size(theme.SizeNameInnerPadding)))
}

func (r *wrappingCheckRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *wrappingCheckRenderer) Refresh() {
	r.WidgetRenderer.Refresh()
	r.label.Text = r.check.Text
	r.label.Importance = widget.MediumImportance
	if r.check.Disabled() {
		r.label.Importance = widget.LowImportance
	}
	r.label.Refresh()
	r.Layout(r.check.Size())
}
