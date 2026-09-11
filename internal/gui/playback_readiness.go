//go:build !(android || ios)

package gui

import (
	"path/filepath"
	"strings"

	ttwidget "github.com/alexballas/fyne-tooltip/widget"
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/driver/desktop"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/theme"
)

// Keep the status on one line; reveal the filename only when truncated.
type playbackStatusLabel struct {
	*ttwidget.Label
	filename string
}

func newPlaybackStatusLabel(text string) *playbackStatusLabel {
	label := &playbackStatusLabel{Label: ttwidget.NewLabel(text)}
	label.ExtendBaseWidget(label)
	return label
}

func (l *playbackStatusLabel) MouseIn(e *desktop.MouseEvent) {
	l.SetToolTip("")
	available := l.Size().Width - 2*theme.SizeForWidget(theme.SizeNameInnerPadding, l)
	width := fyne.MeasureText(l.Text, theme.SizeForWidget(theme.SizeNameText, l), l.TextStyle).Width
	if l.filename != "" && available > 0 && width > available {
		l.SetToolTip(l.filename)
	}
	l.Label.MouseIn(e)
}

// Called on the UI thread; keep all playback slots present in every state.
func (s *FyneScreen) refreshPlaybackReadiness() {
	if s.playbackStatus == nil {
		return
	}
	state := s.getScreenState()
	active := state == "Playing" || state == "Paused"
	status := lang.L("Ready to cast")
	toolTip := ""
	switch {
	case active:
		status = lang.L(state)
	case s.selectedDevice.addr == "":
		status = lang.L("Select a device")
		toolTip = status
	case s.mediafile == "" && s.MediaText.Text == "" && !s.Screencast && !(s.rtmpServerCheck != nil && s.rtmpServerCheck.Checked):
		status = lang.L("Choose media to cast")
		toolTip = status
		s.PlayPause.Disable()
	}
	if s.playPauseToolTip != nil {
		s.playPauseToolTip.SetToolTip(toolTip)
	}
	device := s.selectedDevice
	if active {
		if playingDevice := s.getActiveDevice(); playingDevice.addr != "" {
			device = playingDevice
		}
	}
	if device.addr != "" {
		status += " · " + device.name
	}
	s.playbackStatus.filename = ""
	s.playbackStatus.SetToolTip("")
	if mediaPath := s.nowPlayingPath(); mediaPath != "" {
		s.playbackStatus.filename = filepath.Base(mediaPath)
		status += " · " + s.playbackStatus.filename
	}
	s.playbackStatus.SetText(status)
	if !s.renderGate.remoteLeaseHeld() && s.Stop != nil {
		if active || strings.HasPrefix(s.castingMediaType, "image/") || s.Screencast || s.rtmpServer != nil {
			s.Stop.Enable()
		} else {
			s.Stop.Disable()
		}
	}
	if s.SlideBar != nil {
		if active && !strings.HasPrefix(s.castingMediaType, "image/") && !s.Screencast && s.rtmpServer == nil && !s.renderGate.remoteLeaseHeld() {
			s.SlideBar.Enable()
		} else {
			s.SlideBar.Disable()
		}
	}
}
