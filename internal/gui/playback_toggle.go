//go:build !(android || ios)

package gui

import (
	ttwidget "github.com/alexballas/fyne-tooltip/widget"
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/driver/desktop"
)

// playbackToggle keeps Check's state and callbacks, with a compact switch surface.
type playbackToggle struct {
	ttwidget.Check
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
	if c := fyne.CurrentApp().Driver().CanvasForObject(t); c != nil {
		c.Focus(t)
	}
	t.SetChecked(!t.Checked)
}

func (t *playbackToggle) FocusGained() {
	if t.Disabled() {
		return
	}
	t.focused = true
	t.Check.FocusGained()
}
func (t *playbackToggle) FocusLost() { t.focused = false; t.Check.FocusLost() }
func (t *playbackToggle) MouseIn(e *desktop.MouseEvent) {
	t.Check.ToolTipWidgetExtend.MouseIn(e)
	t.hovered = true
	t.Refresh()
}
func (t *playbackToggle) MouseOut() {
	t.Check.ToolTipWidgetExtend.MouseOut()
	t.hovered = false
	t.Refresh()
}
func (t *playbackToggle) MouseMoved(e *desktop.MouseEvent) { t.Check.ToolTipWidgetExtend.MouseMoved(e) }

func (t *playbackToggle) CreateRenderer() fyne.WidgetRenderer {
	return newPlaybackToggleRenderer(&t.Check.Check, &t.toggleLook)
}

func playbackAutoplayIcon() fyne.Resource {
	return fyne.NewStaticResource("playback-autoplay.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#000" d="M3 4a1 1 0 0 1 1.6-.8l9 6.8a1 1 0 0 1 0 1.6l-9 6.8A1 1 0 0 1 3 17.6V4zM16 3h2v11h-2z"/><path fill="#000" d="M18 15l5 4-5 4v-3h-7v-2h7v-3z"/></svg>`))
}

func playbackDesktopIcon() fyne.Resource {
	return fyne.NewStaticResource("playback-desktop.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#000" fill-rule="evenodd" d="M3 3h18a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-7v2h4v2H6v-2h4v-2H3a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2zm0 2v12h18V5H3z"/><path fill="#000" d="M10 7l6 4-6 4V7z"/></svg>`))
}

func playbackServerIcon() fyne.Resource {
	return fyne.NewStaticResource("playback-server.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path fill="#000" fill-rule="evenodd" d="M4 2h16a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2zm0 2v5h16V4H4zm0 9h16a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-5a2 2 0 0 1 2-2zm0 2v5h16v-5H4z"/><path fill="#000" d="M6 5h2v3H6zm4 1h8v1h-8zM6 16h2v3H6zm4 1h8v1h-8z"/></svg>`))
}
