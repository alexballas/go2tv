package gui

import (
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/layout"
	"github.com/alexballas/refyne/v2/theme"
)

func newTorrentDialogContent(objects ...fyne.CanvasObject) *container.Scroll {
	return container.NewVScroll(container.New(&torrentFormLayout{}, objects...))
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
