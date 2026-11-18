package devices

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

type policyOutcome int

const (
	fetched policyOutcome = iota
	hiddenStrict
	rejected
)

// The discovery policy decides which unsolicited SSDP LOCATIONs go2tv fetches
// before the user selects anything. Real renderer shapes must pass; anything
// that could make go2tv contact a host other than the replying one, or a public
// address, must not in strict mode. Relaxed mode may only fetch what strict
// mode hides; everything rejected stays rejected.
func TestSelectLocationsPolicy(t *testing.T) {
	longPath := func(n int) string {
		base := "http://192.168.1.42:8080/"
		return base + strings.Repeat("a", n-len(base))
	}
	tt := []struct {
		name     string
		location string
		from     net.Addr
		want     policyOutcome // strict; relaxed fetches what strict hides
	}{
		{"samsung", "http://192.168.1.20:9197/dmr", udpFrom("192.168.1.20"), fetched},
		{"sonos", "http://192.168.1.21:1400/xml/device_description.xml", udpFrom("192.168.1.21"), fetched},
		{"sony", "http://192.168.1.22:52323/dmr.xml", udpFrom("192.168.1.22"), fetched},
		{"xbox query", "http://192.168.1.23:2869/upnphost/udhisapi.dll?content=uuid:4ec1f2c5-8a3f-4d3e-9a1b-0b8d1c2e3f40", udpFrom("192.168.1.23"), fetched},
		{"implicit port", "http://192.168.1.24/desc.xml", udpFrom("192.168.1.24"), fetched},
		{"https", "https://192.168.1.25:8443/desc.xml", udpFrom("192.168.1.25"), fetched},
		{"percent-encoded path", "http://192.168.1.26:8080/dev/%E5%AE%A2%E5%8E%85.xml", udpFrom("192.168.1.26"), fetched},
		{"autoip link-local", "http://169.254.10.5:8080/desc.xml", udpFrom("169.254.10.5"), fetched},
		{"cgnat", "http://100.64.0.5:8080/desc.xml", udpFrom("100.64.0.5"), fetched},
		{"longest allowed location", longPath(1024), udpFrom("192.168.1.42"), fetched},

		{"public-IP LAN renderer", "http://192.0.2.1:8080/desc.xml", udpFrom("192.0.2.1"), hiddenStrict},
		{"multi-homed renderer", "http://192.168.1.50:49152/description.xml", udpFrom("192.168.1.51"), hiddenStrict},
		{"proxying vlan relay", "http://192.168.20.15:9197/dmr", udpFrom("192.168.10.1"), hiddenStrict},
		{"nil source", "http://192.168.1.31:8080/desc.xml", nil, hiddenStrict},

		{"public target from another host", "http://8.8.8.8/x", udpFrom("192.168.1.99"), rejected},
		{"location too long", longPath(1025), udpFrom("192.168.1.42"), rejected},
		{"hostname", "http://tv.example/desc.xml", udpFrom("192.168.1.32"), rejected},
		{"localhost", "http://localhost:8080/desc.xml", udpFrom("127.0.0.1"), rejected},
		{"octal loopback", "http://0177.0.0.1:8080/desc.xml", udpFrom("127.0.0.1"), rejected},
		{"octal private", "http://010.0.0.1/x", udpFrom("8.0.0.1"), rejected},
		{"hex loopback", "http://0x7f.1/x", udpFrom("127.0.0.1"), rejected},
		{"decimal loopback", "http://2130706433/x", udpFrom("127.0.0.1"), rejected},
		{"zero-padded", "http://192.168.001.005/x", udpFrom("192.168.1.5"), rejected},
		{"loopback with matching source", "http://127.0.0.1:8080/desc.xml", udpFrom("127.0.0.1"), rejected},
		{"metadata with matching source", "http://169.254.169.254/latest/meta-data/", udpFrom("169.254.169.254"), rejected},
		{"azure wireserver", "http://168.63.129.16/machine", udpFrom("168.63.129.16"), rejected},
		{"multicast", "http://239.255.255.250:1900/x", udpFrom("239.255.255.250"), rejected},
		{"broadcast", "http://255.255.255.255/x", udpFrom("255.255.255.255"), rejected},
		{"unspecified", "http://0.0.0.0:8080/x", udpFrom("0.0.0.0"), rejected},
		{"ipv6 literal", "http://[fe80::1]:8080/x", udpFrom("192.168.1.33"), rejected},
		{"userinfo", "http://user:pass@192.168.1.34/x", udpFrom("192.168.1.34"), rejected},
		{"ftp", "ftp://192.168.1.35/x", udpFrom("192.168.1.35"), rejected},
		{"zero-padded port", "http://192.168.1.36:08009/x", udpFrom("192.168.1.36"), rejected},
		{"port zero", "http://192.168.1.37:0/x", udpFrom("192.168.1.37"), rejected},
		{"port out of range", "http://192.168.1.38:65536/x", udpFrom("192.168.1.38"), rejected},
		{"empty port", "http://192.168.1.39:/x", udpFrom("192.168.1.39"), rejected},
		{"line feed", "http://192.168.1.40/x\n• Evil [http://10.0.0.1/]", udpFrom("192.168.1.40"), rejected},
		{"space", "http://192.168.1.41/a b", udpFrom("192.168.1.41"), rejected},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			replies := []ssdp.Service{{Type: ssdp.RootDevice, Location: tc.location, From: tc.from}}
			for _, relaxed := range []bool{false, true} {
				sel := selectLocations(replies, relaxed)
				want := tc.want
				if relaxed && want == hiddenStrict {
					want = fetched
				}
				got := rejected
				switch {
				case len(sel.selected) == 1:
					got = fetched
				case slices.Equal(sel.hidden, []string{tc.location}):
					got = hiddenStrict
				}
				if got != want || (want == rejected && len(sel.rejected) != 1) {
					t.Fatalf("relaxed=%v: outcome = %d, want %d (location %q)", relaxed, got, want, tc.location)
				}
				if u, _ := url.Parse(tc.location); got == fetched && sel.selected[0].addr.String() != u.Hostname() {
					t.Fatalf("pinned %v, want the LOCATION address %s", sel.selected[0].addr, u.Hostname())
				}
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
	}, false)
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
			sel := selectLocations(tc.replies, false)
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

// Relaxed mode reopens a request primitive for any LAN host, so what strict
// mode hides stays bounded per scan and never pushes out strict-eligible
// devices.
func TestSelectLocationsRelaxedCaps(t *testing.T) {
	realTV := ssdp.Service{Location: "http://192.168.1.20:9197/dmr", From: udpFrom("192.168.1.20")}
	replies := make([]ssdp.Service, 0, 301)
	for i := range 300 {
		ip := fmt.Sprintf("10.1.%d.%d", i/250, i%250+1)
		replies = append(replies, ssdp.Service{Type: "urn:schemas-upnp-org:device:MediaRenderer:1", Location: "http://" + ip + ":8080/x", From: udpFrom("192.168.1.66")})
	}
	replies = append(replies, realTV)

	sel := selectLocations(replies, true)
	got := selectedLocations(sel)
	if len(got) != maxRelaxedPerScan+1 || !slices.Contains(got, realTV.Location) {
		t.Fatalf("selected %d locations (real TV %v), want %d including the real TV", len(got), slices.Contains(got, realTV.Location), maxRelaxedPerScan+1)
	}
}

// The setting is read per scan: flipping it changes what the next scan fetches,
// and a strict scan reports how many locations it hid (shown by -l and the GUI).
func TestRelaxedDiscoveryAppliesToNextScan(t *testing.T) {
	origSearch := ssdpSearch
	origLoad := loadDiscoveredDevices
	t.Cleanup(func() {
		ssdpSearch = origSearch
		loadDiscoveredDevices = origLoad
		SetRelaxedDiscovery(false)
	})

	const relayed = "http://192.168.20.15:9197/dmr"
	ssdpSearch = func(string, int, string) ([]ssdp.Service, error) {
		return []ssdp.Service{{Location: relayed, From: udpFrom("192.168.10.1")}}, nil
	}
	loadDiscoveredDevices = func(_ context.Context, loc string, _ netip.Addr) ([]*soapcalls.DMRextracted, error) {
		return []*soapcalls.DMRextracted{{FriendlyName: "VLAN TV", AvtransportControlURL: "http://192.168.20.15:9197/control"}}, nil
	}

	tt := []struct {
		relaxed    bool
		wantDevice bool
		wantHidden int
	}{
		{false, false, 1},
		{true, true, 0},
		{false, false, 1},
	}
	for _, tc := range tt {
		SetRelaxedDiscovery(tc.relaxed)
		devs, _ := LoadSSDPservices(1)
		if got := len(devs) == 1; got != tc.wantDevice {
			t.Fatalf("relaxed=%v: device listed = %v, want %v", tc.relaxed, got, tc.wantDevice)
		}
		if got := len(HiddenSSDPLocations()); got != tc.wantHidden {
			t.Fatalf("relaxed=%v: hidden = %d, want %d", tc.relaxed, got, tc.wantHidden)
		}
	}
}

// Mismatch and rejection lines carry attacker-chosen text, so they must not
// repeat every scan nor change faster than once per interval.
func TestDiscoveryLimitedSummaryfBoundsChanges(t *testing.T) {
	var buf bytes.Buffer
	SetDiscoveryLogOutput(&buf)
	now := time.Unix(0, 0)
	origNow := discoveryNow
	discoveryNow = func() time.Time { return now }
	t.Cleanup(func() {
		SetDiscoveryLogOutput(nil)
		discoveryNow = origNow
	})

	discoveryLimitedSummaryf("k", "line %d", 1)
	discoveryLimitedSummaryf("k", "line %d", 1)
	for i := 2; i <= 50; i++ {
		now = now.Add(3 * time.Second)
		discoveryLimitedSummaryf("k", "line %d", i)
	}
	now = now.Add(discoveryLimitedInterval)
	discoveryLimitedSummaryf("k", "line %d", 99)
	// An unchanged line after a skipped change must still de-duplicate.
	discoveryLimitedSummaryf("k", "line %d", 99)
	now = now.Add(discoveryLimitedInterval)
	discoveryLimitedSummaryf("k", "line %d", 100)

	got := strings.TrimSpace(buf.String())
	want := strings.Join([]string{
		"discovery: line 1",
		"discovery: k repeated=1",
		"discovery: line 21 skipped_changes=19",
		"discovery: line 41 skipped_changes=19",
		"discovery: line 99 skipped_changes=9",
		"discovery: k repeated=1",
		"discovery: line 100",
	}, "\n")
	if got != want {
		t.Fatalf("log =\n%s\nwant\n%s", got, want)
	}
}

// Server mode logs the hidden count at normal level; it must not repeat every
// scan, nor change faster than once per interval, since replies are remote.
func TestNoticeHiddenFiresOnChangeOnly(t *testing.T) {
	now := time.Unix(0, 0)
	origNow := discoveryNow
	discoveryNow = func() time.Time { return now }
	var got []int
	SetHiddenNoticeFunc(func(n int) { got = append(got, n) })
	t.Cleanup(func() {
		SetHiddenNoticeFunc(nil)
		discoveryNow = origNow
	})

	for _, step := range []struct {
		n       int
		advance time.Duration
	}{
		{0, 0},                    // nothing hidden: silent
		{2, 3 * time.Second},      // first hidden count: logged
		{2, 3 * time.Second},      // unchanged: silent
		{5, 3 * time.Second},      // changed within the interval: deferred
		{5, hiddenNoticeInterval}, // still 5 after the interval: logged
		{0, 3 * time.Second},      // cleared: silent
		{2, hiddenNoticeInterval}, // hidden again: logged
	} {
		now = now.Add(step.advance)
		noticeHidden(step.n)
	}
	if want := []int{2, 5, 2}; !slices.Equal(got, want) {
		t.Fatalf("notices = %v, want %v", got, want)
	}
}

// mDNS answers are unsolicited: one host must not fill the Chromecast cache and
// keep real devices out, cached devices must keep updating, and entries whose
// advertiser went quiet must eventually expire.
func TestChromecastCacheBounds(t *testing.T) {
	ccMu.Lock()
	origDevices, origRound := chromeCastDevices, ccRound
	chromeCastDevices, ccRound = make(map[string]castDevice), 0
	ccMu.Unlock()
	t.Cleanup(func() {
		ccMu.Lock()
		chromeCastDevices, ccRound = origDevices, origRound
		ccMu.Unlock()
	})
	advertise := func(ip net.IP, port int, name string) {
		upsertChromecastFromMDNSEntry(&mdns.ServiceEntry{Name: name + "._googlecast._tcp.local.", AddrV4: ip, Port: port, InfoFields: []string{"fn=" + name}})
	}
	cached := func(address string) (castDevice, bool) {
		ccMu.Lock()
		defer ccMu.Unlock()
		d, ok := chromeCastDevices[address]
		return d, ok
	}

	flooder := net.IPv4(192, 168, 1, 66)
	for port := range maxChromecastsPerIP + 4 {
		advertise(flooder, 9000+port, "flood")
	}
	if _, ok := cached(fmt.Sprintf("192.168.1.66:%d", 9000+maxChromecastsPerIP)); ok {
		t.Fatal("per-IP cap not enforced")
	}
	advertise(net.IPv4(192, 168, 1, 30), 8009, "Living Room")
	if _, ok := cached("192.168.1.30:8009"); !ok {
		t.Fatal("real Chromecast kept out by a flooding host")
	}
	advertise(net.IPv4(192, 168, 1, 30), 8009, "Renamed")
	if d, _ := cached("192.168.1.30:8009"); d.Name != "Renamed" {
		t.Fatalf("update of cached entry refused: name = %q", d.Name)
	}

	for i := range maxChromecasts {
		advertise(net.IPv4(10, 2, byte(i>>8), byte(i)), 8009, "many")
	}
	ccMu.Lock()
	total := len(chromeCastDevices)
	ccMu.Unlock()
	if total != maxChromecasts {
		t.Fatalf("cache size = %d, want cap %d", total, maxChromecasts)
	}

	// The real device keeps answering; everything else goes quiet.
	for range chromecastExpiryRounds + 1 {
		advertise(net.IPv4(192, 168, 1, 30), 8009, "Renamed")
		finishChromecastRound()
	}
	if _, ok := cached("192.168.1.30:8009"); !ok {
		t.Fatal("answering Chromecast expired")
	}
	if _, ok := cached("192.168.1.66:9000"); ok {
		t.Fatal("silent entry did not expire")
	}
}
