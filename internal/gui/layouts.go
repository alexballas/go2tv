package gui

import (
	"fyne.io/fyne/v2"
)

func (d *mainButtonsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := float32(0), float32(0)
	for _, o := range objects {
		childSize := o.MinSize()
		w += childSize.Width
		h = childSize.Height * d.scale
	}
	return fyne.NewSize(w, h)
}

func (d *mainButtonsLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	pos := fyne.NewPos(0, 0)

	bigButtonSize := containerSize.Width
	for q, o := range objects {
		if q != 0 && q != len(objects)-1 {
			bigButtonSize = bigButtonSize - o.MinSize().Width
		}
	}
	bigButtonSize = bigButtonSize / 2

	for q, o := range objects {
		var size fyne.Size
		switch q {
		case 0, len(objects) - 1:
			size = fyne.NewSize(bigButtonSize, o.MinSize().Height*d.scale)
		default:
			size = fyne.NewSize(o.MinSize().Width, o.MinSize().Height*d.scale)
		}
		o.Resize(size)
		o.Move(pos)

		pos = pos.Add(fyne.NewPos(size.Width, 0))
	}
}
