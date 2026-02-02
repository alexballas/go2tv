package gui

import (
	"fyne.io/fyne/v2"
)

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
