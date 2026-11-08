//go:build android || ios

package gui

import (
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/widget"
)

// playbackToggle is the desktop icon/label/switch toggle for touch screens.
type playbackToggle struct {
	widget.Check
	toggleLook
}

func newPlaybackToggle(text string, icon fyne.Resource) *playbackToggle {
	t := &playbackToggle{toggleLook: toggleLook{icon: icon}}
	t.Text = text
	t.ExtendBaseWidget(t)
	return t
}

func (t *playbackToggle) MinSize() fyne.Size { return t.BaseWidget.MinSize() }

func (t *playbackToggle) Tapped(*fyne.PointEvent) {
	if t.Disabled() {
		return
	}
	t.SetChecked(!t.Checked)
}

func (t *playbackToggle) CreateRenderer() fyne.WidgetRenderer {
	return newPlaybackToggleRenderer(&t.Check, &t.toggleLook)
}
