package gui

import (
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/layout"
	"github.com/alexballas/refyne/v2/theme"
)

const (
	torrentDialogMaxWidth          = float32(520)
	torrentDialogHorizontalPadding = float32(32) // Fyne dialog content padding.
)

func newTorrentDialogContent(objects ...fyne.CanvasObject) *container.Scroll {
	return container.NewVScroll(container.New(&torrentFormLayout{}, objects...))
}

func resizeTorrentDialog(s *FyneScreen, d dialog.Dialog, content *container.Scroll) {
	size := s.Current.Canvas().Size()
	if canvas, ok := s.Current.Canvas().(interface {
		PopUpArea() (fyne.Position, fyne.Size)
	}); ok {
		_, size = canvas.PopUpArea()
	}
	width := min(torrentDialogMaxWidth, size.Width-2*theme.Padding())
	// Measure wrapped rows at the width the dialog will actually allocate.
	formWidth := width - torrentDialogHorizontalPadding - theme.InnerPadding()
	content.Content.Resize(fyne.NewSize(formWidth, content.Content.MinSize().Height))
	chromeHeight := d.MinSize().Height - content.MinSize().Height
	height := min(content.Content.MinSize().Height, max(float32(32), size.Height-2*theme.Padding()-chromeHeight))
	content.SetMinSize(fyne.NewSize(0, height))
	d.Resize(fyne.NewSize(width, height+chromeHeight))
}

// Wrapped text must measure at its allocated width before placing the next row.
type torrentFormLayout struct{}

func (*torrentFormLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return layout.NewVBoxLayout().MinSize(objects)
}

func (*torrentFormLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	y := float32(0)
	for _, object := range objects {
		if !object.Visible() {
			continue
		}
		object.Resize(fyne.NewSize(size.Width, object.MinSize().Height))
		height := object.MinSize().Height
		object.Resize(fyne.NewSize(size.Width, height))
		object.Move(fyne.NewPos(0, y))
		y += height + theme.Padding()
	}
}
