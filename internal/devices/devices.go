package devices

import (
	"fmt"
	"sort"

	"github.com/koron/go-ssdp"
	"github.com/pkg/errors"
)

var (
	// Devices map to maintain a list of all the discovered devices.
	Devices = make(map[string]string)
)

// LoadSSDPservices .
func LoadSSDPservices(delay int) error {
	// Reset device list every time we call this.
	Devices = make(map[string]string)
	list, err := ssdp.Search(ssdp.All, delay, "")
	if err != nil {
		return fmt.Errorf("LoadSSDPservices search error: %w", err)
	}

	for _, srv := range list {
		// We only care about the AVTransport services for basic actions
		// (stop,play,pause). If we need support other functionalities
		// like volume control we need to use the RenderingControl service.
		if srv.Type == "urn:schemas-upnp-org:service:AVTransport:1" {
			Devices[srv.Server] = srv.Location
		}
	}

	if len(Devices) > 0 {
		return nil
	}

	return errors.New("loadSSDPservices: No available Media Renderers")
}

// DevicePicker .
func DevicePicker(i int) (string, error) {
	if i > len(Devices) || len(Devices) == 0 || i <= 0 {
		return "", errors.New("devicePicker: Requested device not available")
	}

	keys := make([]string, 0)
	for k := range Devices {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for q, k := range keys {
		if i == q+1 {
			return Devices[k], nil
		}
	}
	return "", errors.New("devicePicker: Something went terribly wrong")
}
