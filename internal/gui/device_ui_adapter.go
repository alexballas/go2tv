//go:build !(android || ios)

package gui

import (
	"fyne.io/fyne/v2"
	"github.com/alexballas/go2tv/devices"
)

// lockFFmpegControls disables the TranscodeCheckBox (used for Chromecast devices)
func lockFFmpegControls(screen *FyneScreen) {
	fyne.Do(func() {
		screen.TranscodeCheckBox.Disable()
	})
}

// unlockFFmpegControls enables the TranscodeCheckBox (used for DLNA devices)
func unlockFFmpegControls(screen *FyneScreen) {
	fyne.Do(func() {
		screen.TranscodeCheckBox.Enable()
	})
}

// setTranscodeForDeviceType automatically adjusts transcode settings

// setTranscodeForDeviceType automatically adjusts transcode settings
// based on the selected device type
func setTranscodeForDeviceType(screen *FyneScreen, deviceType string) {
	if screen == nil || screen.TranscodeCheckBox == nil {
		return
	}

	switch deviceType {
	case devices.DeviceTypeChromecast:
		// Store current transcode preference before changing it
		screen.previousTranscodePref = screen.Transcode

		// Force enable transcode for Chromecast and lock it
		screen.Transcode = true
		screen.TranscodeCheckBox.SetChecked(true)
		screen.TranscodeCheckBox.Refresh()
		lockFFmpegControls(screen)

	case devices.DeviceTypeDLNA:
		// Restore previous transcode preference for DLNA
		screen.Transcode = screen.previousTranscodePref
		screen.TranscodeCheckBox.SetChecked(screen.previousTranscodePref)
		screen.TranscodeCheckBox.Refresh()
		unlockFFmpegControls(screen)

	default:
		// Unknown device type, unlock controls
		unlockFFmpegControls(screen)
	}
}
