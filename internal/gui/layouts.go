package gui

import (
	"fyne.io/fyne/v2"
)

func (d *mainButtonsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := float32(0), float32(0)
	for _, o := range objects {
		childSize := o.MinSize()
		w += childSize.Width
		h = childSize.Height * d.buttonHeight
	}
	return fyne.NewSize(w, h)
}

func (d *mainButtonsLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	pos := fyne.NewPos(0, 0)

	bigButtonSize := containerSize.Width
	for q, o := range objects {
		if q != 0 && q != len(objects)-1 {
			bigButtonSize = bigButtonSize - o.MinSize().Width - d.buttonPadding
		}
	}
	bigButtonSize = (bigButtonSize - d.buttonPadding) / 2

	for q, o := range objects {
		var size fyne.Size
		switch q {
		case 0, len(objects) - 1:
			size = fyne.NewSize(bigButtonSize, o.MinSize().Height*d.buttonHeight)
		default:
			size = fyne.NewSize(o.MinSize().Width, o.MinSize().Height*d.buttonHeight)
		}
		o.Resize(size)
		o.Move(pos)

		pos = pos.Add(fyne.NewPos(size.Width+d.buttonPadding, 0))
	}
}

type RatioLayout struct {
	LeftRatio float32
}

func (l *RatioLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}
	min0 := objects[0].MinSize()
	min1 := objects[1].MinSize()

	w := fyne.Max(min0.Width/l.LeftRatio, min1.Width/(1-l.LeftRatio))
	h := fyne.Max(min0.Height, min1.Height)

	return fyne.NewSize(w, h)
}

func (l *RatioLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}
	leftSize := fyne.NewSize(size.Width*l.LeftRatio, size.Height)
	rightSize := fyne.NewSize(size.Width*(1-l.LeftRatio), size.Height)

	objects[0].Resize(leftSize)
	objects[0].Move(fyne.NewPos(0, 0))

	objects[1].Resize(rightSize)
	objects[1].Move(fyne.NewPos(leftSize.Width, 0))
}
