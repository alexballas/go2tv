//go:build !(android || ios)

package gui

import (
	"strings"

	"github.com/alexballas/refyne/v2/lang"
)

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
	if s.selectedDevice.addr != "" {
		status += " · " + s.selectedDevice.name
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
