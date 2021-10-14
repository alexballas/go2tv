package gui

import "fyne.io/fyne/v2"

func (d *mainButtonsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := float32(0), float32(0)
	for _, o := range objects {
		childSize := o.MinSize()
		w += childSize.Width
		h = childSize.Height
	}
	return fyne.NewSize(w, h)
}

func (d *mainButtonsLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	pos := fyne.NewPos(0, 0)

	bigButtonSize := containerSize.Width
	for q, o := range objects {
		z := q + 1
		if z%2 == 0 {
			bigButtonSize = bigButtonSize - o.MinSize().Width
		}
	}
	bigButtonSize = bigButtonSize / 2

	for q, o := range objects {
		var size fyne.Size
		switch q % 2 {
		case 0:
			size = fyne.NewSize(bigButtonSize, o.MinSize().Height)
		default:
			size = o.MinSize()
		}
		o.Resize(size)
		o.Move(pos)

		pos = pos.Add(fyne.NewPos(size.Width, 0))
	}
}
