package devices

import (
	"context"
	"errors"
	"testing"

	"github.com/alexballas/go-ssdp"
	"go2tv.app/go2tv/v2/soapcalls"
)

func TestLoadSSDPservicesDetectsFromNonAVTransportST(t *testing.T) {
	origSearch := ssdpSearch
	origLoad := loadDevicesFromLocation
	t.Cleanup(func() {
		ssdpSearch = origSearch
		loadDevicesFromLocation = origLoad
	})

	ssdpSearch = func(searchType string, waitSec int, localAddr string) ([]ssdp.Service, error) {
		return []ssdp.Service{
			{
				Type:     ssdp.RootDevice,
				Location: "http://sonos.local:1400/xml/device_description.xml",
			},
		}, nil
	}

	loadDevicesFromLocation = func(ctx context.Context, dmrurl string) ([]*soapcalls.DMRextracted, error) {
		if dmrurl != "http://sonos.local:1400/xml/device_description.xml" {
			t.Fatalf("unexpected location: %s", dmrurl)
		}

		return []*soapcalls.DMRextracted{
			{
				FriendlyName:          "Sonos One",
				AvtransportControlURL: "http://sonos.local:1400/MediaRenderer/AVTransport/Control",
				ConnectionManagerURL:  "http://sonos.local:1400/MediaRenderer/ConnectionManager/Control",
			},
		}, nil
	}

	devs, err := LoadSSDPservices(1)
	if err != nil {
		t.Fatalf("LoadSSDPservices() err = %v, want nil", err)
	}

	if len(devs) != 1 {
		t.Fatalf("LoadSSDPservices() len = %d, want 1", len(devs))
	}

	if devs[0].Name != "Sonos One" {
		t.Fatalf("LoadSSDPservices() name = %q, want %q", devs[0].Name, "Sonos One")
	}

	if devs[0].Addr != "http://sonos.local:1400/xml/device_description.xml" {
		t.Fatalf("LoadSSDPservices() addr = %q, want location URL", devs[0].Addr)
	}
}

func TestLoadSSDPservicesFiltersDevicesMissingConnectionManager(t *testing.T) {
	origSearch := ssdpSearch
	origLoad := loadDevicesFromLocation
	t.Cleanup(func() {
		ssdpSearch = origSearch
		loadDevicesFromLocation = origLoad
	})

	ssdpSearch = func(searchType string, waitSec int, localAddr string) ([]ssdp.Service, error) {
		return []ssdp.Service{
			{
				Type:     ssdp.RootDevice,
				Location: "http://speaker.local:1400/xml/device_description.xml",
			},
		}, nil
	}

	loadDevicesFromLocation = func(ctx context.Context, dmrurl string) ([]*soapcalls.DMRextracted, error) {
		return []*soapcalls.DMRextracted{
			{
				FriendlyName:          "Broken Renderer",
				AvtransportControlURL: "http://speaker.local:1400/MediaRenderer/AVTransport/Control",
			},
		}, nil
	}

	_, err := LoadSSDPservices(1)
	if !errors.Is(err, ErrNoDeviceAvailable) {
		t.Fatalf("LoadSSDPservices() err = %v, want %v", err, ErrNoDeviceAvailable)
	}
}
