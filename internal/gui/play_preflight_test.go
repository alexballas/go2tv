package gui

import (
	"testing"

	"go2tv.app/go2tv/v2/devices"
)

func TestPlaybackPreflight(t *testing.T) {
	tests := []struct {
		name          string
		state         string
		hasMedia      bool
		deviceType    string
		deviceAddress string
		controlURL    string
		want          playbackPreflightFailure
	}{
		{
			name:  "active playback needs no new inputs",
			state: "Playing",
			want:  playbackPreflightReady,
		},
		{
			name:       "new playback needs media",
			deviceType: devices.DeviceTypeDLNA,
			controlURL: "http://renderer/control",
			want:       playbackPreflightMissingMedia,
		},
		{
			name:       "new DLNA playback needs device",
			hasMedia:   true,
			deviceType: devices.DeviceTypeDLNA,
			want:       playbackPreflightMissingDevice,
		},
		{
			name:       "new DLNA playback is ready",
			hasMedia:   true,
			deviceType: devices.DeviceTypeDLNA,
			controlURL: "http://renderer/control",
			want:       playbackPreflightReady,
		},
		{
			name:       "new Chromecast playback needs device",
			hasMedia:   true,
			deviceType: devices.DeviceTypeChromecast,
			want:       playbackPreflightMissingDevice,
		},
		{
			name:          "new Chromecast playback is ready",
			hasMedia:      true,
			deviceType:    devices.DeviceTypeChromecast,
			deviceAddress: "192.0.2.10",
			want:          playbackPreflightReady,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := playbackPreflight(
				test.state,
				test.hasMedia,
				test.deviceType,
				test.deviceAddress,
				test.controlURL,
			)
			if got != test.want {
				t.Fatalf("playbackPreflight() = %v, want %v", got, test.want)
			}
		})
	}
}
