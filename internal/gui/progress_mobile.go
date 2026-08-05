//go:build android || ios

package gui

import (
	"net/url"
	"strings"

	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
)

func (s *FyneScreen) rendererPermit(bool) (func(), bool) {
	return func() {}, true
}

func (s *FyneScreen) persistResumeProgress(int, float64, bool) {}

func chromecastDeviceHost(device devType) string {
	if device.deviceType != devices.DeviceTypeChromecast || device.addr == "" {
		return ""
	}

	u, err := url.Parse(device.addr)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func chromecastClientOwnsDevice(client *castprotocol.CastClient, device devType) bool {
	if client == nil {
		return false
	}

	deviceHost := chromecastDeviceHost(device)
	if deviceHost == "" {
		return false
	}
	return strings.EqualFold(client.Host(), deviceHost)
}

func (s *FyneScreen) chromecastSessionClient() *castprotocol.CastClient {
	client := s.chromecastClient
	if client == nil || !client.IsConnected() {
		return nil
	}
	if !chromecastClientOwnsDevice(client, s.getActiveDevice()) {
		return nil
	}
	return client
}

func (s *FyneScreen) activeChromecastPlaybackClient() *castprotocol.CastClient {
	switch s.getScreenState() {
	case "Playing", "Paused":
		return s.chromecastSessionClient()
	default:
		return nil
	}
}
