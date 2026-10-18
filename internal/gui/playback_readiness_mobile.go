//go:build android || ios

package gui

import (
	"fmt"
	"strings"

	"github.com/alexballas/refyne/v2/lang"

	"go2tv.app/go2tv/v2/devices"
)

func (s *FyneScreen) mobilePlaybackBusy() bool {
	return s.mobilePlaybackStarting() || s.playbackStartupPending() || s.dlnaPlaybackPending()
}

// playbackRequirement names what Play still needs, or "" when ready.
func (s *FyneScreen) playbackRequirement() string {
	hasMedia := s.mediafile != nil || (s.MediaText != nil && s.MediaText.Text != "")
	switch playbackPreflight(s.getScreenState(), hasMedia, s.selectedDeviceType, s.selectedDevice.addr, s.controlURL) {
	case playbackPreflightMissingMedia:
		return lang.L("Choose media to cast")
	case playbackPreflightMissingDevice:
		if s.connectingDevice != "" && s.connectingDevice == s.selectedDevice.addr {
			return fmt.Sprintf(lang.L("Connecting to %s…"), s.selectedDevice.name)
		}
		return lang.L("Select a device")
	}
	return ""
}

// refreshPlaybackReadiness re-evaluates Play after media or device changes.
func (s *FyneScreen) refreshPlaybackReadiness() {
	setPlayPauseView("", s)
}

// radioPlayerLines returns the song (else the station) and a "Station ·
// Device" detail line for a selected radio station.
func (s *FyneScreen) radioPlayerLines() (title, detail string, ok bool) {
	if s.MediaText == nil {
		return "", "", false
	}
	station, song, _, ok := s.radioNowPlaying(s.MediaText.Text)
	if !ok {
		return "", "", false
	}
	parts := []string{station.Name}
	title = song
	if title == "" {
		title, parts = station.Name, nil
	}
	if device := s.getActiveDevice().name; device != "" {
		parts = append(parts, device)
	}
	return title, strings.Join(parts, " · "), true
}

// Called on the UI thread. The player bar's top row shows the timeline, the
// radio song for live stations, or what Play still needs while idle, so the
// buttons below never move.
func (s *FyneScreen) refreshPlaybackStatus() {
	if s.timeline == nil || s.playbackStatus == nil || s.radioRow == nil {
		return
	}
	state := s.getScreenState()
	playing := state == "Playing" || state == "Paused"
	busy := s.mobilePlaybackBusy()
	active := playing || s.dlnaSeekRestart || busy
	if s.Stop != nil {
		if active {
			s.Stop.Enable()
		} else {
			s.Stop.Disable()
		}
	}
	volume := s.volumeAvailable()
	for _, control := range s.volumeControls {
		if volume {
			control.Enable()
		} else {
			control.Disable()
		}
	}
	if s.DeviceList != nil {
		s.DeviceList.Refresh()
	}

	song := s.radioSong.label
	// Startup has no timeline or song yet; say so instead of a blank row.
	if busy && !playing {
		status := lang.L("Loading media…")
		if s.selectedDevice.name != "" {
			status += " · " + s.selectedDevice.name
		}
		s.playbackStatus.SetText(status)
		s.timeline.Hide()
		s.radioRow.Hide()
		s.playbackStatus.Show()
		return
	}
	if active {
		s.playbackStatus.Hide()
		if title, detail, ok := s.radioPlayerLines(); ok {
			song.SetText(title)
			s.radioDetail.SetText(detail)
			s.timeline.Hide()
			s.radioRow.Show()
			return
		}
		s.radioRow.Hide()
		s.timeline.Show()
		return
	}
	status := s.playbackRequirement()
	if status == "" {
		status = lang.L("Ready to cast") + " · " + s.selectedDevice.name
	}
	s.playbackStatus.SetText(status)
	s.timeline.Hide()
	s.radioRow.Hide()
	s.playbackStatus.Show()
}

// clearDeviceSelection forgets the selected device. Called on the UI thread.
func (s *FyneScreen) clearDeviceSelection() {
	s.selectedDevice = devType{}
	s.selectedDeviceType = ""
	s.connectingDevice = ""
	s.controlURL, s.eventlURL, s.renderingControlURL, s.connectionManagerURL = "", "", "", ""
	if s.DeviceList != nil {
		s.DeviceList.UnselectAll()
	}
}

// volumeAvailable reports whether volume commands have a reachable target.
func (s *FyneScreen) volumeAvailable() bool {
	if s.getActiveDevice().addr != "" {
		return true
	}
	if s.selectedDeviceType == devices.DeviceTypeChromecast {
		return s.selectedDevice.addr != ""
	}
	return s.renderingControlURL != ""
}
