package devices

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alexballas/go-ssdp"
)

// SSDP discovery policy.
//
// Discovery fetches device descriptions before the user selects anything, from
// LOCATION URLs that any LAN host can put in an SSDP reply. A LOCATION is
// fetched only if its host is a canonical IPv4 literal (no DNS), the address is
// not loopback, unspecified, multicast, broadcast or a cloud metadata address,
// and it equals the UDP source of a reply that carried it. The fetch dials only
// that address. URLs the user supplies (-t) and post-selection paths do not go
// through this policy.
//
// Residual risk: a LAN host able to forge UDP source addresses can still cause
// up to maxLocationsPerScan blind GETs per scan, repeated every scan, to any
// routable address that is not denylisted. Kernels drop forged packets that
// claim a local source, which keeps loopback and our own addresses out of
// reach (Linux, by documentation only).

const (
	maxLocationsPerScan   = 128
	maxLocationsPerSource = 16
	maxDeviceNameRunes    = 128
	unknownDeviceName     = "Unknown Device"
)

var (
	errLocationInvalid    = errors.New("location invalid")
	errLocationHost       = errors.New("location host not a canonical IPv4 literal")
	errLocationPort       = errors.New("location port invalid")
	errLocationAddrDenied = errors.New("location address denied")

	broadcastAddr = netip.AddrFrom4([4]byte{255, 255, 255, 255})
	metadataAddrs = []netip.Addr{
		netip.AddrFrom4([4]byte{169, 254, 169, 254}), // AWS, GCP, Azure, OpenStack
		netip.AddrFrom4([4]byte{169, 254, 170, 2}),   // AWS ECS task metadata
		netip.AddrFrom4([4]byte{100, 100, 100, 200}), // Alibaba Cloud
	}
)

// blockedDiscoveryAddr reports whether discovery must never contact ip.
func blockedDiscoveryAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return !ip.IsValid() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip == broadcastAddr || slices.Contains(metadataAddrs, ip)
}

// validateDiscoveredLocation checks everything about an SSDP LOCATION except
// the source match, and returns the address to pin.
func validateDiscoveredLocation(raw string) (netip.Addr, error) {
	for i := range len(raw) {
		if c := raw[i]; c < 0x21 || c > 0x7e || c == '[' || c == ']' {
			return netip.Addr{}, errLocationInvalid
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Scheme != "http" && u.Scheme != "https" {
		return netip.Addr{}, errLocationInvalid
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !ip.Is4() {
		return netip.Addr{}, errLocationHost
	}
	if port := u.Port(); port != "" || strings.HasSuffix(u.Host, ":") {
		n, err := strconv.Atoi(port)
		if err != nil || port[0] == '0' || n < 1 || n > 65535 {
			return netip.Addr{}, errLocationPort
		}
	}
	if blockedDiscoveryAddr(ip) {
		return netip.Addr{}, errLocationAddrDenied
	}
	return ip, nil
}

// replySource returns the UDP source of an SSDP reply, or the zero Addr.
func replySource(from net.Addr) netip.Addr {
	ua, ok := from.(*net.UDPAddr)
	if !ok {
		return netip.Addr{}
	}
	ip, ok := netip.AddrFromSlice(ua.IP)
	if !ok {
		return netip.Addr{}
	}
	return ip.Unmap()
}

// hasRendererEvidence reports whether a reply advertises a MediaRenderer or
// AVTransport. Replies are attacker-controlled, so this only orders candidates
// under the caps; it is not a security check.
func hasRendererEvidence(srv ssdp.Service) bool {
	for _, v := range []string{srv.Type, srv.USN} {
		v = strings.ToLower(v)
		if strings.Contains(v, ":device:mediarenderer:") || strings.Contains(v, ":service:avtransport:") {
			return true
		}
	}
	return false
}

type discoveredLocation struct {
	location string
	addr     netip.Addr
}

type locationSelection struct {
	selected         []discoveredLocation
	seen             int
	rejectedInvalid  int
	rejectedMismatch int
	capped           int
}

// selectLocations applies the discovery policy to SSDP replies and returns the
// locations to fetch: deduplicated, renderer evidence first, then arrival
// order, within the per-source and per-scan caps. A location passes if any
// reply that carried it came from its host.
func selectLocations(list []ssdp.Service) locationSelection {
	type candidate struct {
		discoveredLocation
		err      error
		matched  bool
		renderer bool
	}
	var (
		order []*candidate
		byLoc = make(map[string]*candidate)
	)
	for _, srv := range list {
		if srv.Location == "" {
			continue
		}
		c, ok := byLoc[srv.Location]
		if !ok {
			c = &candidate{discoveredLocation: discoveredLocation{location: srv.Location}}
			c.addr, c.err = validateDiscoveredLocation(srv.Location)
			byLoc[srv.Location] = c
			order = append(order, c)
		}
		if c.err == nil && replySource(srv.From) == c.addr {
			c.matched = true
		}
		if hasRendererEvidence(srv) {
			c.renderer = true
		}
	}

	sel := locationSelection{seen: len(order)}
	var passed []*candidate
	for _, c := range order {
		switch {
		case c.err != nil:
			sel.rejectedInvalid++
			discoveryDebugf("SSDP location rejected location=%q reason=%q", c.location, c.err)
		case !c.matched:
			sel.rejectedMismatch++
			discoveryDebugf("SSDP location rejected location=%q reason=%q", c.location, "reply source differs from location host")
		default:
			passed = append(passed, c)
		}
	}
	slices.SortStableFunc(passed, func(a, b *candidate) int {
		switch {
		case a.renderer == b.renderer:
			return 0
		case a.renderer:
			return -1
		default:
			return 1
		}
	})

	perSource := make(map[netip.Addr]int)
	for _, c := range passed {
		if len(sel.selected) >= maxLocationsPerScan || perSource[c.addr] >= maxLocationsPerSource {
			sel.capped++
			continue
		}
		perSource[c.addr]++
		sel.selected = append(sel.selected, c.discoveredLocation)
	}
	return sel
}

// sanitizeDeviceName makes a device-supplied name safe to show on one line:
// control, format and line/paragraph separator runes become spaces (zero-width
// joiners stay, emoji and many scripts need them), and the name is capped. It
// does not collapse or trim, so legitimate names are left unchanged.
func sanitizeDeviceName(name string) string {
	var b strings.Builder
	n := 0
	for _, r := range name {
		if n == maxDeviceNameRunes {
			break
		}
		if unsafeNameRune(r) {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	out := b.String()
	if strings.TrimSpace(out) == "" {
		return unknownDeviceName
	}
	return out
}

func unsafeNameRune(r rune) bool {
	switch r {
	case '‌', '‍':
		return false
	case utf8.RuneError:
		return true
	}
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp)
}
