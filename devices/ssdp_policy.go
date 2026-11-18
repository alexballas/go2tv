package devices

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
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
// Mismatched locations are hidden and only logged. Relaxed discovery
// (SetRelaxedDiscovery, opt-in) fetches up to maxMismatchedPerScan of them too.
//
// Residual risk: a LAN host able to forge UDP source addresses can still cause
// up to maxLocationsPerScan blind GETs per scan, repeated every scan, to any
// routable address that is not denylisted. Kernels drop forged packets that
// claim a local source, which keeps loopback and our own addresses out of
// reach (Linux, by documentation only). With relaxed discovery no forging is
// needed: any LAN host gets up to maxMismatchedPerScan such GETs per scan,
// including to our own LAN addresses.

const (
	maxLocationsPerScan   = 128
	maxLocationsPerSource = 16
	maxMismatchedPerScan  = 16
	maxDeviceNameRunes    = 128
	maxLoggedItems        = 8
	maxLoggedSources      = 4
	unknownDeviceName     = "Unknown Device"
)

var (
	errLocationInvalid    = errors.New("location invalid")
	errLocationHost       = errors.New("location host not a canonical IPv4 literal")
	errLocationPort       = errors.New("location port invalid")
	errLocationAddrDenied = errors.New("location address denied")

	relaxedDiscovery atomic.Bool
	hiddenLocations  atomic.Int64

	broadcastAddr = netip.AddrFrom4([4]byte{255, 255, 255, 255})
	metadataAddrs = []netip.Addr{
		netip.AddrFrom4([4]byte{169, 254, 169, 254}), // AWS, GCP, Azure, OpenStack
		netip.AddrFrom4([4]byte{169, 254, 170, 2}),   // AWS ECS task metadata
		netip.AddrFrom4([4]byte{100, 100, 100, 200}), // Alibaba Cloud
	}
)

// SetRelaxedDiscovery turns off the SSDP source check for later scans: a
// LOCATION is then fetched even when no reply carrying it came from its host.
// This restores devices behind proxying relays or with several addresses, but
// lets any LAN host make go2tv contact other addresses. Every other rule stays.
func SetRelaxedDiscovery(enabled bool) { relaxedDiscovery.Store(enabled) }

// RelaxedDiscovery reports whether the SSDP source check is off.
func RelaxedDiscovery() bool { return relaxedDiscovery.Load() }

// HiddenSSDPLocations returns how many SSDP LOCATIONs the last scan left
// unfetched because no reply carrying them came from their host.
func HiddenSSDPLocations() int { return int(hiddenLocations.Load()) }

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

type rejectedLocation struct {
	location string
	reason   error
}

type mismatchedLocation struct {
	location string
	addr     netip.Addr
	sources  []netip.Addr
	renderer bool
}

type locationSelection struct {
	selected   []discoveredLocation
	seen       int
	rejected   []rejectedLocation
	mismatched []mismatchedLocation
	// hidden counts mismatched locations left unfetched by the source check.
	hidden int
	capped int
}

// selectLocations applies the discovery policy to SSDP replies and returns the
// locations to fetch: deduplicated, source-matched first, then renderer
// evidence, then arrival order, within the caps. A location matches if any
// reply that carried it came from its host. With relaxed, mismatched locations
// are fetched too, at most maxMismatchedPerScan of them.
func selectLocations(list []ssdp.Service, relaxed bool) locationSelection {
	type candidate struct {
		discoveredLocation
		err      error
		matched  bool
		renderer bool
		sources  []netip.Addr
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
		src := replySource(srv.From)
		if c.err == nil && src == c.addr {
			c.matched = true
		}
		if src.IsValid() && len(c.sources) < maxLoggedSources && !slices.Contains(c.sources, src) {
			c.sources = append(c.sources, src)
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
			sel.rejected = append(sel.rejected, rejectedLocation{location: c.location, reason: c.err})
		case !c.matched:
			sel.mismatched = append(sel.mismatched, mismatchedLocation{location: c.location, addr: c.addr, sources: c.sources, renderer: c.renderer})
			if !relaxed {
				sel.hidden++
				continue
			}
			passed = append(passed, c)
		default:
			passed = append(passed, c)
		}
	}
	slices.SortStableFunc(passed, func(a, b *candidate) int {
		if a.matched != b.matched {
			if a.matched {
				return -1
			}
			return 1
		}
		if a.renderer != b.renderer {
			if a.renderer {
				return -1
			}
			return 1
		}
		return 0
	})

	perSource := make(map[netip.Addr]int)
	mismatchedFetched := 0
	for _, c := range passed {
		if len(sel.selected) >= maxLocationsPerScan || perSource[c.addr] >= maxLocationsPerSource ||
			!c.matched && mismatchedFetched >= maxMismatchedPerScan {
			sel.capped++
			continue
		}
		if !c.matched {
			mismatchedFetched++
		}
		perSource[c.addr]++
		sel.selected = append(sel.selected, c.discoveredLocation)
	}
	return sel
}

// logSelection reports policy rejections and source mismatches once per scan.
// Entries are sorted and capped so a stable network logs a stable line, which
// the summary logger then de-duplicates; changes from remote hosts are
// rate-limited so an attacker cannot flood the log.
func logSelection(sel locationSelection, relaxed bool) {
	if len(sel.rejected) > 0 {
		items := make([]string, 0, len(sel.rejected))
		for _, r := range sel.rejected {
			items = append(items, fmt.Sprintf("%q reason=%q", r.location, r.reason))
		}
		discoveryLimitedSummaryf("SSDP locations rejected", "SSDP locations rejected count=%d items=%s", len(items), formatLogItems(items))
	}
	if len(sel.mismatched) > 0 {
		action := "hidden"
		if relaxed {
			action = "fetched"
		}
		items := make([]string, 0, len(sel.mismatched))
		for _, m := range sel.mismatched {
			sources := make([]string, 0, len(m.sources))
			for _, src := range m.sources {
				sources = append(sources, src.String())
			}
			items = append(items, fmt.Sprintf("%q ip=%s from=%s renderer=%t", m.location, m.addr, strings.Join(sources, ","), m.renderer))
		}
		discoveryLimitedSummaryf("SSDP source mismatch", "SSDP source mismatch relaxed=%t action=%s count=%d items=%s", relaxed, action, len(items), formatLogItems(items))
	}
}

func formatLogItems(items []string) string {
	slices.Sort(items)
	if len(items) > maxLoggedItems {
		return strings.Join(items[:maxLoggedItems], "; ") + fmt.Sprintf("; +%d more", len(items)-maxLoggedItems)
	}
	return strings.Join(items, "; ")
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
	case '\u200c', '\u200d':
		return false
	case utf8.RuneError:
		return true
	}
	return unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp)
}
