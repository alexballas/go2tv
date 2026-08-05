package gui

import "go2tv.app/go2tv/v2/devices"

type playbackPreflightFailure uint8

const (
	playbackPreflightReady playbackPreflightFailure = iota
	playbackPreflightMissingMedia
	playbackPreflightMissingDevice
)

// playbackPreflight checks the inputs that can reject a new Play request before
// Android is asked to start a foreground service. Existing playback is allowed
// through because the same button controls pause and resume.
func playbackPreflight(
	state string,
	hasMedia bool,
	deviceType string,
	deviceAddress string,
	controlURL string,
) playbackPreflightFailure {
	if state == "Playing" || state == "Paused" {
		return playbackPreflightReady
	}
	if !hasMedia {
		return playbackPreflightMissingMedia
	}
	if deviceType == devices.DeviceTypeChromecast {
		if deviceAddress == "" {
			return playbackPreflightMissingDevice
		}
		return playbackPreflightReady
	}
	if controlURL == "" {
		return playbackPreflightMissingDevice
	}
	return playbackPreflightReady
}
