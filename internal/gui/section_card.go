//go:build !(android || ios)

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

const (
	sectionCardRadius          float32 = 12
	sectionCardMargin          float32 = 6
	sectionCardPadding         float32 = 10
	sectionCardVerticalPadding float32 = 8
)

// sectionCard styles desktop categories without changing other cards
// or the padding of the controls they contain.
type sectionCard struct {
	widget.BaseWidget
	title   string
	content fyne.CanvasObject
}

func newSectionCard(title string, content fyne.CanvasObject) *sectionCard {
	c := &sectionCard{title: title, content: content}
	c.ExtendBaseWidget(c)
	return c
}

func (c *sectionCard) CreateRenderer() fyne.WidgetRenderer {
	background := canvas.NewRectangle(color.Transparent)
	background.CornerRadius = sectionCardRadius
	background.StrokeWidth = 1
	header := canvas.NewText(c.title, color.Transparent)
	header.TextStyle.Bold = true
	body := container.NewBorder(header, nil, nil, nil, c.content)
	inset := container.New(layout.NewCustomPaddedLayout(
		sectionCardVerticalPadding, sectionCardVerticalPadding, sectionCardPadding, sectionCardPadding,
	), body)
	// Each panel supplies its own margin, including at the column boundary and
	// when the responsive layout stacks the devices below the controls.
	panel := container.New(layout.NewCustomPaddedLayout(
		sectionCardMargin, sectionCardMargin, sectionCardMargin, sectionCardMargin,
	), container.NewStack(background, inset))
	r := &sectionCardRenderer{
		WidgetRenderer: widget.NewSimpleRenderer(panel),
		card:           c,
		background:     background,
		header:         header,
	}
	r.Refresh()
	return r
}

type sectionCardRenderer struct {
	fyne.WidgetRenderer
	card       *sectionCard
	background *canvas.Rectangle
	header     *canvas.Text
}

func (r *sectionCardRenderer) Refresh() {
	th := r.card.Theme()
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	r.background.FillColor = th.Color(theme.ColorNameBackground, variant)
	r.background.StrokeColor = th.Color(theme.ColorNameSeparator, variant)
	r.header.Color = th.Color(theme.ColorNameForeground, variant)
	r.header.TextSize = th.Size(theme.SizeNameText)
	r.WidgetRenderer.Refresh()
}
