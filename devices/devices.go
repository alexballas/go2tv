package devices

import (
	"context"
	"fmt"
	"sort"

	"github.com/alexballas/go-ssdp"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/pkg/errors"
)

var (
	ErrNoDeviceAvailable  = errors.New("loadSSDPservices: No available Media Renderers")
	ErrDeviceNotAvailable = errors.New("devicePicker: Requested device not available")
	ErrSomethingWentWrong = errors.New("devicePicker: Something went terribly wrong")
)

// LoadSSDPservices returns a map with all the devices that support the
// AVTransport service.
func LoadSSDPservices(delay int) (map[string]string, error) {
	// Reset device list every time we call this.
	urlList := make(map[string]string)
	list, err := ssdp.Search(ssdp.All, delay, "239.255.255.250:1900")
	if err != nil {
		return nil, fmt.Errorf("LoadSSDPservices search error: %w", err)
	}

	for _, srv := range list {
		// We only care about the AVTransport services for basic actions
		// (stop,play,pause). If we need support other functionalities
		// like volume control we need to use the RenderingControl service.
		if srv.Type == "urn:schemas-upnp-org:service:AVTransport:1" {
			friendlyName, err := soapcalls.GetFriendlyName(context.Background(), srv.Location)
			if err != nil {
				continue
			}

			urlList[srv.Location] = friendlyName
		}
	}

	deviceList := make(map[string]string)
	dupNames := make(map[string]int)
	for loc, fn := range urlList {
		_, exists := dupNames[fn]
		dupNames[fn]++
		if exists {
			fn = fn + " (" + loc + ")"
		}

		deviceList[fn] = loc
	}

	for fn, c := range dupNames {
		if c > 1 {
			loc := deviceList[fn]
			delete(deviceList, fn)
			fn = fn + " (" + loc + ")"
			deviceList[fn] = loc
		}
	}

	if len(deviceList) > 0 {
		return deviceList, nil
	}

	return nil, ErrNoDeviceAvailable
}

// DevicePicker will pick the nth device from the devices input map.
func DevicePicker(devices map[string]string, n int) (string, error) {
	if n > len(devices) || len(devices) == 0 || n <= 0 {
		return "", ErrDeviceNotAvailable
	}

	var keys []string
	for k := range devices {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for q, k := range keys {
		if n == q+1 {
			return devices[k], nil
		}
	}

	return "", ErrSomethingWentWrong
}
