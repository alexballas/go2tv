//go:build !(android || ios)

package gui

import (
	"path/filepath"
	"strings"

	"github.com/alexballas/refyne/v2/lang"
)

// Called on the UI thread; keep all playback slots present in every state.
func (s *FyneScreen) refreshPlaybackReadiness() {
	if s.playbackTitle == nil {
		return
	}
	title := lang.L("No media selected")
	if s.mediafile != "" {
		title = filepath.Base(s.mediafile)
	}
	if s.ExternalMediaURL.Checked && s.MediaText.Text != "" {
		title = s.MediaText.Text
	}
	s.playbackTitle.SetText(title)
	state := s.getScreenState()
	active := state == "Playing" || state == "Paused"
	status := lang.L("Ready to cast")
	switch {
	case active:
		status = lang.L(state)
	case s.selectedDevice.addr == "":
		status = lang.L("Select a device")
	case s.mediafile == "" && s.MediaText.Text == "" && !s.Screencast && !(s.rtmpServerCheck != nil && s.rtmpServerCheck.Checked):
		status = lang.L("Choose media to cast")
		s.PlayPause.Disable()
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
