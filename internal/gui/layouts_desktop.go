//go:build !(android || ios)

package gui

import (
	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/theme"
)

const (
	settingsLabelWidth   float32 = 260
	settingsControlWidth float32 = 430
	settingsColumnGap    float32 = 16
	settingsPanelWidth   float32 = 950
)

type settingsPanelLayout struct{}

func (settingsPanelLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return objects[0].MinSize()
}

func (settingsPanelLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	content := objects[0]
	width := fyne.Min(size.Width, fyne.Max(content.MinSize().Width, settingsPanelWidth))
	content.Move(fyne.NewPos((size.Width-width)/2, 0))
	content.Resize(fyne.NewSize(width, size.Height))
}

type settingsRowLayout struct {
	stacked bool
}

func (l *settingsRowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	label, control := objects[0].MinSize(), objects[1].MinSize()
	height := fyne.Max(label.Height, control.Height)
	if l.stacked {
		height = label.Height + theme.Padding() + control.Height
	}
	return fyne.NewSize(fyne.Max(label.Width, control.Width), height)
}

func (l *settingsRowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	label, control := objects[0], objects[1]
	labelMin, controlMin := label.MinSize(), control.MinSize()
	labelWidth := fyne.Max(settingsLabelWidth, labelMin.Width)
	controlX := labelWidth + settingsColumnGap
	l.stacked = size.Width < fyne.Max(560, controlX+controlMin.Width)
	label.Move(fyne.NewPos(0, 0))
	if l.stacked {
		label.Resize(fyne.NewSize(size.Width, labelMin.Height))
		control.Move(fyne.NewPos(0, labelMin.Height+theme.Padding()))
		control.Resize(fyne.NewSize(size.Width, controlMin.Height))
		return
	}
	label.Resize(fyne.NewSize(labelWidth, size.Height))
	controlWidth := fyne.Min(size.Width-controlX, fyne.Max(settingsControlWidth, controlMin.Width))
	control.Move(fyne.NewPos(size.Width-controlWidth, 0))
	control.Resize(fyne.NewSize(controlWidth, size.Height))
}

func mainTabWindowSize(w fyne.Window, tabs *container.AppTabs, main *container.Scroll) fyne.Size {
	padding := float32(0)
	if w.Padded() {
		padding = 2 * theme.Padding()
	}
	width := fyne.Max(1120, tabs.MinSize().Width+padding)
	// Resolve responsive columns at the starting width before measuring height.
	// Scroll.MinSize only describes its viewport, not the controls it contains.
	tabs.Resize(fyne.NewSize(width-padding, 700))
	tabs.Refresh()
	tabBarHeight := tabs.Size().Height - main.Size().Height
	height := main.Content.MinSize().Height + tabBarHeight + theme.Padding()
	return fyne.NewSize(width, fyne.Max(height, tabs.MinSize().Height)+padding)
}

// Keep mode switches grouped with fixed gaps and compact hit areas.
type playbackModesLayout struct{}

const playbackControlsGap float32 = 14

func (playbackModesLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) == 0 {
		return fyne.NewSize(0, 0)
	}
	size := fyne.NewSize(0, 0)
	for _, object := range objects {
		size.Width += object.MinSize().Width
		size.Height = fyne.Max(size.Height, object.MinSize().Height)
	}
	return fyne.NewSize(size.Width+playbackControlsGap*float32(len(objects)-1), size.Height)
}

func (playbackModesLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	x := float32(0)
	for _, object := range objects {
		width := object.MinSize().Width
		object.Move(fyne.NewPos(x, 0))
		object.Resize(fyne.NewSize(width, size.Height))
		x += width + playbackControlsGap
	}
}
