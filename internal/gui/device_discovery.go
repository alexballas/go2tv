package gui

import (
	"fmt"
	"net/url"
	"sort"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/utils"
)

type devType struct {
	name                     string
	addr                     string
	deviceType               string
	isAudioOnly              bool
	requiresRelaxedDiscovery bool
}

func (d devType) hiddenByDiscoveryPolicy() bool {
	return d.requiresRelaxedDiscovery && !devices.RelaxedDiscovery()
}

func getDevices() ([]devType, error) {
	deviceList, err := devices.LoadAllDevices()
	if err != nil {
		return nil, fmt.Errorf("getDevices error: %w", err)
	}

	var guiDeviceList []devType
	for _, dev := range deviceList {
		guiDeviceList = append(guiDeviceList, devType{
			name:                     dev.Name,
			addr:                     dev.Addr,
			deviceType:               dev.Type,
			isAudioOnly:              dev.IsAudioOnly,
			requiresRelaxedDiscovery: dev.RequiresRelaxedDiscovery,
		})
	}
	return guiDeviceList, nil
}

// Retain reachable devices across missed discovery replies.
func retainReachableDevices(current, previous []devType) []devType {
outer:
	for _, old := range previous {
		if old.hiddenByDiscoveryPolicy() {
			continue
		}
		oldAddress, _ := url.Parse(old.addr)
		for _, seen := range current {
			newAddress, _ := url.Parse(seen.addr)
			if newAddress.Host == oldAddress.Host {
				continue outer
			}
		}
		if utils.HostPortIsAlive(oldAddress.Host) {
			current = append(current, old)
		}
	}
	return current
}

// Keep a hidden renderer selected while it is casting so its controls keep
// working; the selection clears once that session ends.
func (s *FyneScreen) clearHiddenDeviceSelection() {
	if !s.selectedDevice.hiddenByDiscoveryPolicy() || s.getActiveDevice().addr == s.selectedDevice.addr {
		return
	}
	s.clearDeviceSelection()
	s.refreshPlaybackReadiness()
}

// sortDevTypeSlice sorts devices alphabetically by name,
// with DLNA devices before Chromecast devices when names are equal.
func sortDevTypeSlice(d []devType) {
	sort.Slice(d, func(i, j int) bool {
		if d[i].deviceType != d[j].deviceType {
			return d[i].deviceType < d[j].deviceType
		}
		return d[i].name < d[j].name
	})
}
