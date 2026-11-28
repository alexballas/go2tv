package devices

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/alexballas/go-ssdp"
	"go2tv.app/go2tv/v2/soapcalls"
)

func TestLoadSSDPServicesDetectsNonAVTransportST(t *testing.T) {
	origSearch := ssdpSearch
	origLoad := loadDiscoveredDevices
	t.Cleanup(func() {
		ssdpSearch = origSearch
		loadDiscoveredDevices = origLoad
	})

	ssdpSearch = func(searchType string, waitSec int, localAddr string) ([]ssdp.Service, error) {
		return []ssdp.Service{
			{
				Type:     ssdp.RootDevice,
				Location: "http://192.168.1.40:1400/xml/device_description.xml",
				From:     &net.UDPAddr{IP: net.IPv4(192, 168, 1, 40), Port: 1900},
			},
		}, nil
	}

	loadDiscoveredDevices = func(ctx context.Context, dmrurl string, pinned netip.Addr) ([]*soapcalls.DMRextracted, error) {
		if dmrurl != "http://192.168.1.40:1400/xml/device_description.xml" || pinned != netip.MustParseAddr("192.168.1.40") {
			t.Fatalf("unexpected location: %s pinned %v", dmrurl, pinned)
		}

		return []*soapcalls.DMRextracted{
			{
				FriendlyName:          "Sonos One",
				AvtransportControlURL: "http://192.168.1.40:1400/MediaRenderer/AVTransport/Control",
				ConnectionManagerURL:  "http://192.168.1.40:1400/MediaRenderer/ConnectionManager/Control",
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

	if devs[0].Addr != "http://192.168.1.40:1400/xml/device_description.xml" {
		t.Fatalf("LoadSSDPservices() addr = %q, want location URL", devs[0].Addr)
	}
}

func TestLoadSSDPServicesAllowsMissingConnectionManager(t *testing.T) {
	origSearch := ssdpSearch
	origLoad := loadDiscoveredDevices
	t.Cleanup(func() {
		ssdpSearch = origSearch
		loadDiscoveredDevices = origLoad
	})

	ssdpSearch = func(searchType string, waitSec int, localAddr string) ([]ssdp.Service, error) {
		return []ssdp.Service{
			{
				Type:     ssdp.RootDevice,
				Location: "http://192.168.1.41:1400/xml/device_description.xml",
				From:     &net.UDPAddr{IP: net.IPv4(192, 168, 1, 41), Port: 1900},
			},
		}, nil
	}

	loadDiscoveredDevices = func(ctx context.Context, dmrurl string, pinned netip.Addr) ([]*soapcalls.DMRextracted, error) {
		return []*soapcalls.DMRextracted{
			{
				FriendlyName:          "Legacy Renderer",
				AvtransportControlURL: "http://192.168.1.41:1400/MediaRenderer/AVTransport/Control",
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

	if devs[0].Name != "Legacy Renderer" {
		t.Fatalf("LoadSSDPservices() name = %q, want %q", devs[0].Name, "Legacy Renderer")
	}
}

func TestDiscoverySummaryfSuppressesDuplicates(t *testing.T) {
	var buf bytes.Buffer
	SetDiscoveryLogOutput(&buf)
	t.Cleanup(func() {
		SetDiscoveryLogOutput(nil)
	})

	discoverySummaryf("LoadAllDevices summary", "LoadAllDevices summary delay=%d combined=%d", 1, 2)
	discoverySummaryf("LoadAllDevices summary", "LoadAllDevices summary delay=%d combined=%d", 1, 2)
	discoverySummaryf("LoadAllDevices summary", "LoadAllDevices summary delay=%d combined=%d", 1, 2)
	discoverySummaryf("LoadAllDevices summary", "LoadAllDevices summary delay=%d combined=%d", 2, 3)

	got := strings.TrimSpace(buf.String())
	want := strings.Join([]string{
		"discovery: LoadAllDevices summary delay=1 combined=2",
		"discovery: LoadAllDevices summary repeated=2",
		"discovery: LoadAllDevices summary delay=2 combined=3",
	}, "\n")

	if got != want {
		t.Fatalf("discoverySummaryf() output = %q, want %q", got, want)
	}
}

func TestFormatSummaryListDedupesAndCaps(t *testing.T) {
	got := formatSummaryList([]string{"b", "a", "a", "c"}, 2)
	if got != "a,b,+1 more" {
		t.Fatalf("formatSummaryList() = %q, want %q", got, "a,b,+1 more")
	}
}

func TestSSDPSearchRetriesWhenPortIsBusy(t *testing.T) {
	original := ssdpSearch
	t.Cleanup(func() { ssdpSearch = original })

	var attempted []string
	ssdpSearch = func(_ string, _ int, addr string) ([]ssdp.Service, error) {
		attempted = append(attempted, addr)
		if len(attempted) == 1 {
			return nil, &net.OpError{Op: "listen", Net: "udp4", Err: errors.New("address already in use")}
		}
		return []ssdp.Service{{Location: "http://192.168.1.40/device.xml"}}, nil
	}

	found, addr, err := searchSSDP(t.Context(), 1)
	if err != nil {
		t.Fatalf("searchSSDP: %v", err)
	}
	if len(found) != 1 || addr != "0.0.0.0:3340" ||
		!slices.Equal(attempted, []string{"0.0.0.0:3339", "0.0.0.0:3340"}) {
		t.Fatalf("search returned %d devices at %s after trying %v", len(found), addr, attempted)
	}
}

func TestLoadAllDevicesReturnsCachedResults(t *testing.T) {
	origDLNA := append([]Device(nil), dlnaDevices...)
	ccMu.Lock()
	origChromecast := make(map[string]castDevice, len(chromeCastDevices))
	maps.Copy(origChromecast, chromeCastDevices)
	ccMu.Unlock()
	t.Cleanup(func() {
		setDLNADevices(origDLNA)
		ccMu.Lock()
		chromeCastDevices = origChromecast
		ccMu.Unlock()
	})

	setDLNADevices([]Device{{
		Name: "Bedroom TV",
		Addr: "http://192.168.1.20:1400/xml/device_description.xml",
		Type: DeviceTypeDLNA,
	}})
	ccMu.Lock()
	chromeCastDevices = map[string]castDevice{
		"192.168.1.30:8009": {Name: "Living Room"},
	}
	ccMu.Unlock()

	devs, err := LoadAllDevices()
	if err != nil {
		t.Fatalf("LoadAllDevices() err = %v, want nil", err)
	}

	if len(devs) != 2 {
		t.Fatalf("LoadAllDevices() len = %d, want 2", len(devs))
	}

	if devs[0].Type != DeviceTypeChromecast || devs[1].Type != DeviceTypeDLNA {
		t.Fatalf("LoadAllDevices() types = %q, %q, want %q then %q", devs[0].Type, devs[1].Type, DeviceTypeChromecast, DeviceTypeDLNA)
	}
}

func TestStartDiscoveryStartsLoopsOnce(t *testing.T) {
	var buf bytes.Buffer
	SetDiscoveryLogOutput(&buf)
	t.Cleanup(func() {
		discoveryStartOnce = sync.Once{}
		SetDiscoveryLogOutput(nil)
	})

	discoveryStartOnce = sync.Once{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	StartDiscovery(ctx)
	StartDiscovery(ctx)

	if got := strings.Count(buf.String(), "discovery: Discovery loops starting\n"); got != 1 {
		t.Fatalf("Discovery loops starting logs = %d, want 1", got)
	}
}
