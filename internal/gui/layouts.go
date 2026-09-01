package gui

import (
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/theme"
)

type responsiveTwoColumnLayout struct {
	breakpoint              float32
	leftRatio               float32
	narrowTrailingMinHeight float32
	stacked                 bool
}

func newResponsiveTwoColumnLayout(breakpoint, leftRatio float32) *responsiveTwoColumnLayout {
	return &responsiveTwoColumnLayout{
		breakpoint: breakpoint,
		leftRatio:  leftRatio,
		stacked:    true,
	}
}

func (l *responsiveTwoColumnLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) < 2 {
		return fyne.NewSize(0, 0)
	}

	leftMin := objects[0].MinSize()
	rightMin := objects[1].MinSize()
	width := fyne.Max(leftMin.Width, rightMin.Width)
	if !l.stacked {
		return fyne.NewSize(width, fyne.Max(leftMin.Height, rightMin.Height))
	}

	rightHeight := fyne.Max(rightMin.Height, l.narrowTrailingMinHeight)
	return fyne.NewSize(width, leftMin.Height+theme.Padding()+rightHeight)
}

func (l *responsiveTwoColumnLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 2 {
		return
	}

	leftMin := objects[0].MinSize()
	rightMin := objects[1].MinSize()
	wideMinWidth := fyne.Max(leftMin.Width/l.leftRatio, rightMin.Width/(1-l.leftRatio))
	l.stacked = size.Width < fyne.Max(l.breakpoint, wideMinWidth)
	if !l.stacked {
		leftWidth := size.Width * l.leftRatio
		objects[0].Move(fyne.NewPos(0, 0))
		objects[0].Resize(fyne.NewSize(leftWidth, size.Height))
		objects[1].Move(fyne.NewPos(leftWidth, 0))
		objects[1].Resize(fyne.NewSize(size.Width-leftWidth, size.Height))
		return
	}

	padding := theme.Padding()
	rightHeight := fyne.Max(rightMin.Height, l.narrowTrailingMinHeight)
	leftHeight := leftMin.Height
	if extra := size.Height - leftHeight - padding - rightHeight; extra > 0 {
		rightHeight += extra
	}

	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(fyne.NewSize(size.Width, leftHeight))
	objects[1].Move(fyne.NewPos(0, leftHeight+padding))
	objects[1].Resize(fyne.NewSize(size.Width, rightHeight))
}
