package devices

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/alexballas/go-ssdp"
	"github.com/hashicorp/mdns"
	"go2tv.app/go2tv/v2/soapcalls"
)

func udpFrom(ip string) net.Addr {
	return &net.UDPAddr{IP: net.ParseIP(ip), Port: 1900}
}

func selectedLocations(sel locationSelection) []string {
	out := make([]string, 0, len(sel.selected))
	for _, d := range sel.selected {
		out = append(out, d.location)
	}
	return out
}

// The discovery policy decides which unsolicited SSDP LOCATIONs go2tv fetches
// before the user selects anything. Real renderer shapes must pass; anything
// that could make go2tv contact a host other than the replying one must not.
func TestSelectLocationsPolicy(t *testing.T) {
	tt := []struct {
		name     string
		location string
		from     net.Addr
		want     bool
	}{
		{"samsung", "http://192.168.1.20:9197/dmr", udpFrom("192.168.1.20"), true},
		{"sonos", "http://192.168.1.21:1400/xml/device_description.xml", udpFrom("192.168.1.21"), true},
		{"sony", "http://192.168.1.22:52323/dmr.xml", udpFrom("192.168.1.22"), true},
		{"xbox query", "http://192.168.1.23:2869/upnphost/udhisapi.dll?content=uuid:4ec1f2c5-8a3f-4d3e-9a1b-0b8d1c2e3f40", udpFrom("192.168.1.23"), true},
		{"implicit port", "http://192.168.1.24/desc.xml", udpFrom("192.168.1.24"), true},
		{"https", "https://192.168.1.25:8443/desc.xml", udpFrom("192.168.1.25"), true},
		{"percent-encoded path", "http://192.168.1.26:8080/dev/%E5%AE%A2%E5%8E%85.xml", udpFrom("192.168.1.26"), true},
		{"routed public address", "http://192.0.2.1:8080/desc.xml", udpFrom("192.0.2.1"), true},
		{"autoip link-local", "http://169.254.10.5:8080/desc.xml", udpFrom("169.254.10.5"), true},
		{"cgnat", "http://100.64.0.5:8080/desc.xml", udpFrom("100.64.0.5"), true},

		{"source mismatch", "http://192.168.1.30:8080/desc.xml", udpFrom("192.168.1.99"), false},
		{"internet target from lan host", "http://8.8.8.8/x", udpFrom("192.168.1.99"), false},
		{"nil source", "http://192.168.1.31:8080/desc.xml", nil, false},
		{"hostname", "http://tv.example/desc.xml", udpFrom("192.168.1.32"), false},
		{"localhost", "http://localhost:8080/desc.xml", udpFrom("127.0.0.1"), false},
		{"octal loopback", "http://0177.0.0.1:8080/desc.xml", udpFrom("127.0.0.1"), false},
		{"octal private", "http://010.0.0.1/x", udpFrom("8.0.0.1"), false},
		{"hex loopback", "http://0x7f.1/x", udpFrom("127.0.0.1"), false},
		{"decimal loopback", "http://2130706433/x", udpFrom("127.0.0.1"), false},
		{"zero-padded", "http://192.168.001.005/x", udpFrom("192.168.1.5"), false},
		{"loopback with matching source", "http://127.0.0.1:8080/desc.xml", udpFrom("127.0.0.1"), false},
		{"metadata with matching source", "http://169.254.169.254/latest/meta-data/", udpFrom("169.254.169.254"), false},
		{"multicast", "http://239.255.255.250:1900/x", udpFrom("239.255.255.250"), false},
		{"broadcast", "http://255.255.255.255/x", udpFrom("255.255.255.255"), false},
		{"unspecified", "http://0.0.0.0:8080/x", udpFrom("0.0.0.0"), false},
		{"ipv6 literal", "http://[fe80::1]:8080/x", udpFrom("192.168.1.33"), false},
		{"userinfo", "http://user:pass@192.168.1.34/x", udpFrom("192.168.1.34"), false},
		{"ftp", "ftp://192.168.1.35/x", udpFrom("192.168.1.35"), false},
		{"zero-padded port", "http://192.168.1.36:08009/x", udpFrom("192.168.1.36"), false},
		{"port zero", "http://192.168.1.37:0/x", udpFrom("192.168.1.37"), false},
		{"port out of range", "http://192.168.1.38:65536/x", udpFrom("192.168.1.38"), false},
		{"empty port", "http://192.168.1.39:/x", udpFrom("192.168.1.39"), false},
		{"line feed", "http://192.168.1.40/x\n• Evil [http://10.0.0.1/]", udpFrom("192.168.1.40"), false},
		{"space", "http://192.168.1.41/a b", udpFrom("192.168.1.41"), false},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			sel := selectLocations([]ssdp.Service{{Type: ssdp.RootDevice, Location: tc.location, From: tc.from}})
			if got := len(sel.selected) == 1; got != tc.want {
				t.Fatalf("selected = %v, want %v (location %q)", got, tc.want, tc.location)
			}
			if tc.want && sel.selected[0].addr != replySource(tc.from) {
				t.Fatalf("pinned = %v, want reply source %v", sel.selected[0].addr, tc.from)
			}
		})
	}
}

// A forged reply carrying a real device's LOCATION must not hide that device,
// and the device must be fetched once however many replies carried it.
func TestSelectLocationsMatchesAnyReply(t *testing.T) {
	loc := "http://192.168.1.20:9197/dmr"
	sel := selectLocations([]ssdp.Service{
		{Location: loc, From: udpFrom("192.168.1.66")},
		{Location: loc, From: udpFrom("192.168.1.20")},
		{Location: loc, From: udpFrom("192.168.1.20")},
	})
	if got := selectedLocations(sel); !slices.Equal(got, []string{loc}) {
		t.Fatalf("selected = %q, want [%q]", got, loc)
	}
}

func TestSelectLocationsCaps(t *testing.T) {
	const renderer = "urn:schemas-upnp-org:device:MediaRenderer:1"
	realTV := ssdp.Service{Type: renderer, Location: "http://192.168.1.20:9197/dmr", From: udpFrom("192.168.1.20")}

	floodOneSource := func(n int, st string) []ssdp.Service {
		out := make([]ssdp.Service, n)
		for i := range out {
			out[i] = ssdp.Service{Type: st, Location: fmt.Sprintf("http://10.0.0.1:%d/x", 1000+i), From: udpFrom("10.0.0.1")}
		}
		return out
	}
	floodManySources := func(n int, st string) []ssdp.Service {
		out := make([]ssdp.Service, n)
		for i := range out {
			ip := fmt.Sprintf("10.1.%d.%d", i/250, i%250+1)
			out[i] = ssdp.Service{Type: st, Location: "http://" + ip + ":8080/x", From: udpFrom(ip)}
		}
		return out
	}

	tt := []struct {
		name         string
		replies      []ssdp.Service
		wantSelected int
		wantTV       bool
	}{
		{"per-source cap", append(floodOneSource(100, ssdp.RootDevice), realTV), maxLocationsPerSource + 1, true},
		{"per-scan cap keeps earlier renderer", append([]ssdp.Service{realTV}, floodManySources(300, renderer)...), maxLocationsPerScan, true},
		{"renderer evidence beats earlier noise", append(floodManySources(300, ssdp.RootDevice), realTV), maxLocationsPerScan, true},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			sel := selectLocations(tc.replies)
			got := selectedLocations(sel)
			if len(got) != tc.wantSelected {
				t.Fatalf("selected %d locations, want %d", len(got), tc.wantSelected)
			}
			if slices.Contains(got, realTV.Location) != tc.wantTV {
				t.Fatalf("real TV selected = %v, want %v", !tc.wantTV, tc.wantTV)
			}
			if sel.capped != sel.seen-len(got) {
				t.Fatalf("capped = %d, want %d", sel.capped, sel.seen-len(got))
			}
		})
	}
}

// End to end: discovery must hand only policy-passing locations to the loader,
// pinned to the replying host, and forged names must not break the list.
func TestLoadSSDPServicesFetchesOnlyTrustedLocations(t *testing.T) {
	origSearch := ssdpSearch
	origLoad := loadDiscoveredDevices
	t.Cleanup(func() {
		ssdpSearch = origSearch
		loadDiscoveredDevices = origLoad
	})

	const trusted = "http://192.168.1.20:9197/dmr"
	ssdpSearch = func(string, int, string) ([]ssdp.Service, error) {
		return []ssdp.Service{
			{Location: "http://127.0.0.1:8080/x", From: udpFrom("192.168.1.66")},
			{Location: "http://0177.0.0.1:8080/x", From: udpFrom("192.168.1.66")},
			{Location: "http://tv.example/x", From: udpFrom("192.168.1.66")},
			{Location: "http://192.168.1.1/admin", From: udpFrom("192.168.1.66")},
			{Location: trusted, From: udpFrom("192.168.1.20")},
		}, nil
	}

	var (
		mu    sync.Mutex
		calls []string
	)
	loadDiscoveredDevices = func(_ context.Context, loc string, pinned netip.Addr) ([]*soapcalls.DMRextracted, error) {
		mu.Lock()
		calls = append(calls, loc+" pinned="+pinned.String())
		mu.Unlock()
		return []*soapcalls.DMRextracted{{
			FriendlyName:          "Forged\tRow\n• Evil [http://10.0.0.1/x.xml]",
			AvtransportControlURL: "http://192.168.1.20:9197/control",
		}}, nil
	}

	devs, err := LoadSSDPservices(1)
	if err != nil {
		t.Fatalf("LoadSSDPservices() err = %v", err)
	}
	// Background discovery loops started by other tests may scan through the
	// same stubs, so assert what was loaded, not how often.
	mu.Lock()
	loaded := slices.Compact(slices.Sorted(slices.Values(calls)))
	mu.Unlock()
	if want := []string{trusted + " pinned=192.168.1.20"}; !slices.Equal(loaded, want) {
		t.Fatalf("loaded = %q, want %q", loaded, want)
	}
	if len(devs) != 1 || strings.ContainsAny(devs[0].Name, "\t\n") {
		t.Fatalf("devices = %+v, want one device with a single-line name", devs)
	}
}

func TestSanitizeDeviceName(t *testing.T) {
	tt := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "[TV] Samsung 7 Series (55)", "[TV] Samsung 7 Series (55)"},
		{"cyrillic", "Телевизор", "Телевизор"},
		{"cjk", "客厅电视", "客厅电视"},
		{"emoji zwj sequence", "Kids 👨\u200d👩\u200d👧 TV", "Kids 👨\u200d👩\u200d👧 TV"},
		{"persian zwnj", "تلویزیون‌اتاق", "تلویزیون‌اتاق"},
		{"spacing kept", "Living  Room ", "Living  Room "},
		{"line breaks", "A\nB\r\nC\tD", "A B  C D"},
		{"escape", "TV\x1b[2J", "TV [2J"},
		{"unicode separators", "A B C\u0085D", "A B C D"},
		{"bidi override", "TV‮exe.", "TV exe."},
		{"invalid utf8", "TV\xff", "TV "},
		{"blank", "", unknownDeviceName},
		{"only controls", "\n\t", unknownDeviceName},
		{"truncated", strings.Repeat("é", 200), strings.Repeat("é", maxDeviceNameRunes)},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeDeviceName(tc.in); got != tc.want {
				t.Fatalf("sanitizeDeviceName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestChromecastDiscoveryPolicy(t *testing.T) {
	tt := []struct {
		name     string
		addr     net.IP
		fn       string
		wantName string
	}{
		{"real device", net.IPv4(192, 0, 2, 40), "Living Room TV", "Living Room TV"},
		{"forged name", net.IPv4(192, 0, 2, 41), "Cast\n• Evil [http://10.0.0.1/]", "Cast • Evil [http://10.0.0.1/]"},
		{"loopback", net.IPv4(127, 0, 0, 1), "Loop", ""},
		{"metadata", net.IPv4(169, 254, 169, 254), "Meta", ""},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			address := net.JoinHostPort(tc.addr.String(), "8009")
			t.Cleanup(func() {
				ccMu.Lock()
				delete(chromeCastDevices, address)
				ccMu.Unlock()
			})

			upsertChromecastFromMDNSEntry(&mdns.ServiceEntry{
				Name:       "x._googlecast._tcp.local.",
				AddrV4:     tc.addr,
				Port:       8009,
				InfoFields: []string{"fn=" + tc.fn},
			})

			ccMu.Lock()
			dev, cached := chromeCastDevices[address]
			ccMu.Unlock()
			if cached != (tc.wantName != "") || dev.Name != tc.wantName {
				t.Fatalf("cached=%v name=%q, want cached=%v name=%q", cached, dev.Name, tc.wantName != "", tc.wantName)
			}
		})
	}
}
