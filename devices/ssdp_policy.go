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
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alexballas/go-ssdp"
)

// SSDP discovery policy.
//
// Discovery fetches device descriptions before the user selects anything, from
// LOCATION URLs that any LAN host can put in an SSDP reply. Every LOCATION must
// be a canonical IPv4 literal (no DNS, no IPv6: the search is IPv4-only, so an
// IPv6 LOCATION can never match its sender) of at most maxLocationBytes
// printable ASCII bytes with no spaces or brackets, and its address must not be
// loopback, unspecified, multicast, broadcast or a cloud metadata address. The
// fetch dials only that address. URLs the user supplies (-t) and
// post-selection paths do not go through this policy.
//
// Strict mode (default) fetches a LOCATION only if its address is private
// (RFC 1918, link-local or CGNAT) and equals the UDP source of a reply that
// carried it. Two kinds are hidden and only logged: a private address whose
// replies came from elsewhere (relays, multi-homed devices), and a
// source-matched public address (public-IP LANs). Locality of public addresses
// cannot be verified the same way on every platform, and a forged reply could
// otherwise make go2tv send a GET through a VPN route the attacker lacks.
// Relaxed discovery (SetRelaxedDiscovery, opt-in) fetches up to
// maxRelaxedPerScan hidden ones per scan too. A public address whose replies
// came from elsewhere is rejected in every mode.
//
// Residual risk: a LAN host able to forge UDP source addresses can still cause
// up to maxLocationsPerScan blind GETs per scan, repeated every scan, to
// private addresses the victim can route to (other VLANs, VPN or container
// networks). Kernels drop forged packets that claim a local source, which keeps
// loopback and our own addresses out of reach (Linux, by documentation only).
// A bridged LAN or proxy ARP extends what counts as local. With relaxed
// discovery no forging is needed: any LAN host gets up to maxRelaxedPerScan
// such GETs per scan, to private addresses or to its own public address.

const (
	maxLocationsPerScan   = 128
	maxLocationsPerSource = 16
	maxRelaxedPerScan     = 16
	maxLocationBytes      = 1024
	maxDeviceNameRunes    = 128
	maxLoggedItems        = 8
	maxLoggedSources      = 4
	unknownDeviceName     = "Unknown Device"

	reasonSourceMismatch = "reply source differs from location host"
	reasonPublicAddress  = "public address"
)

var (
	errLocationInvalid    = errors.New("location invalid")
	errLocationHost       = errors.New("location host not a canonical IPv4 literal")
	errLocationPort       = errors.New("location port invalid")
	errLocationAddrDenied = errors.New("location address denied")
	errLocationTooLong    = errors.New("location too long")
	errPublicMismatch     = errors.New("public address and reply source differs")

	relaxedDiscovery atomic.Bool
	hiddenMu         sync.Mutex
	hiddenLocations  []string

	cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

	broadcastAddr = netip.AddrFrom4([4]byte{255, 255, 255, 255})
	metadataAddrs = []netip.Addr{
		netip.AddrFrom4([4]byte{169, 254, 169, 254}), // AWS, GCP, Azure, OpenStack
		netip.AddrFrom4([4]byte{169, 254, 170, 2}),   // AWS ECS task metadata
		netip.AddrFrom4([4]byte{100, 100, 100, 200}), // Alibaba Cloud
		netip.AddrFrom4([4]byte{168, 63, 129, 16}),   // Azure WireServer (platform infrastructure)
		netip.AddrFrom4([4]byte{169, 254, 170, 23}),  // EKS Pod Identity agent
		netip.AddrFrom4([4]byte{169, 254, 0, 23}),    // Tencent Cloud metadata
		netip.AddrFrom4([4]byte{169, 254, 10, 10}),   // Tencent Cloud metadata
	}
)

// SetRelaxedDiscovery makes later scans also fetch what strict mode hides:
// private addresses whose replies came from elsewhere, and source-matched
// public addresses. This restores devices behind proxying relays, with several
// addresses or on public-IP LANs, but lets any LAN host make go2tv contact
// other addresses. Every other rule stays.
func SetRelaxedDiscovery(enabled bool) { relaxedDiscovery.Store(enabled) }

// RelaxedDiscovery reports whether relaxed discovery is on.
func RelaxedDiscovery() bool { return relaxedDiscovery.Load() }

// HiddenSSDPLocations returns the SSDP description URLs the last scan left
// unfetched because strict mode hides them (relaxed discovery would fetch them).
func HiddenSSDPLocations() []string {
	hiddenMu.Lock()
	defer hiddenMu.Unlock()
	return slices.Clone(hiddenLocations)
}

func setHiddenSSDPLocations(locations []string) {
	hiddenMu.Lock()
	hiddenLocations = locations
	hiddenMu.Unlock()
}

// hiddenNoticeInterval bounds how often the count-only hidden notice repeats.
const hiddenNoticeInterval = time.Minute

var (
	hiddenNoticeMu   sync.Mutex
	hiddenNoticeFunc func(n int)
	hiddenNoticeLast int
	hiddenNoticeAt   time.Time
)

// SetHiddenNoticeFunc installs a callback told how many SSDP description URLs a
// strict scan hid. It fires when the count changes to a non-zero value, at most
// once per hiddenNoticeInterval, so headless operators get a hint without a log
// line per scan. nil removes it.
func SetHiddenNoticeFunc(fn func(n int)) {
	hiddenNoticeMu.Lock()
	hiddenNoticeFunc, hiddenNoticeLast, hiddenNoticeAt = fn, 0, time.Time{}
	hiddenNoticeMu.Unlock()
}

func noticeHidden(n int) {
	hiddenNoticeMu.Lock()
	fn := hiddenNoticeFunc
	now := discoveryNow()
	if fn == nil || n == hiddenNoticeLast || (n > 0 && !hiddenNoticeAt.IsZero() && now.Sub(hiddenNoticeAt) < hiddenNoticeInterval) {
		hiddenNoticeMu.Unlock()
		return
	}
	hiddenNoticeLast = n
	if n == 0 {
		hiddenNoticeMu.Unlock()
		return
	}
	hiddenNoticeAt = now
	hiddenNoticeMu.Unlock()
	fn(n)
}

// privateDiscoveryAddr reports whether ip is in a range strict discovery may
// contact: RFC 1918, IPv4 link-local or CGNAT.
func privateDiscoveryAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsPrivate() || ip.IsLinkLocalUnicast() || cgnatPrefix.Contains(ip)
}

// blockedDiscoveryAddr reports whether discovery must never contact ip.
func blockedDiscoveryAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return !ip.IsValid() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip == broadcastAddr || slices.Contains(metadataAddrs, ip)
}

// validateDiscoveredLocation checks everything about an SSDP LOCATION except
// the source match, and returns the address to pin.
func validateDiscoveredLocation(raw string) (netip.Addr, error) {
	if len(raw) > maxLocationBytes {
		return netip.Addr{}, errLocationTooLong
	}
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

// restrictedLocation is a valid LOCATION that strict mode hides and relaxed
// discovery may fetch.
type restrictedLocation struct {
	location string
	addr     netip.Addr
	sources  []netip.Addr
	renderer bool
	reason   string
}

type locationSelection struct {
	selected   []discoveredLocation
	seen       int
	rejected   []rejectedLocation
	restricted []restrictedLocation
	// hidden lists restricted locations left unfetched because the scan was
	// strict; relaxed discovery would fetch them.
	hidden []string
	// relaxedSelected counts restricted locations selected for fetching in
	// relaxed mode; whether each fetch then succeeds is a load result.
	relaxedSelected int
	capped          int
}

// selectLocations applies the discovery policy to SSDP replies and returns the
// locations to fetch: deduplicated, strict-eligible first, then renderer
// evidence, then arrival order, within the caps. A location matches if any
// reply that carried it came from its host. With relaxed, restricted locations
// are fetched too, at most maxRelaxedPerScan of them.
func selectLocations(list []ssdp.Service, relaxed bool) locationSelection {
	type candidate struct {
		discoveredLocation
		err        error
		matched    bool
		renderer   bool
		sources    []netip.Addr
		restricted bool
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
		private := c.err == nil && privateDiscoveryAddr(c.addr)
		var reason string
		switch {
		case c.err != nil:
			sel.rejected = append(sel.rejected, rejectedLocation{location: c.location, reason: c.err})
			continue
		case !private && !c.matched:
			sel.rejected = append(sel.rejected, rejectedLocation{location: c.location, reason: errPublicMismatch})
			continue
		case private && c.matched:
			passed = append(passed, c)
			continue
		case !c.matched:
			reason = reasonSourceMismatch
		default:
			reason = reasonPublicAddress
		}
		slices.SortFunc(c.sources, netip.Addr.Compare)
		sel.restricted = append(sel.restricted, restrictedLocation{location: c.location, addr: c.addr, sources: c.sources, renderer: c.renderer, reason: reason})
		if !relaxed {
			sel.hidden = append(sel.hidden, c.location)
			continue
		}
		c.restricted = true
		passed = append(passed, c)
	}
	slices.SortStableFunc(passed, func(a, b *candidate) int {
		if a.restricted != b.restricted {
			if !a.restricted {
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
	for _, c := range passed {
		if len(sel.selected) >= maxLocationsPerScan || perSource[c.addr] >= maxLocationsPerSource ||
			c.restricted && sel.relaxedSelected >= maxRelaxedPerScan {
			sel.capped++
			continue
		}
		if c.restricted {
			sel.relaxedSelected++
		}
		perSource[c.addr]++
		sel.selected = append(sel.selected, c.discoveredLocation)
	}
	slices.Sort(sel.hidden)
	return sel
}

// logSelection reports policy rejections and strict-mode restrictions once per
// scan. Entries are sorted and capped so a stable network logs a stable line,
// which the summary logger then de-duplicates; changes from remote hosts are
// rate-limited so an attacker cannot flood the log.
func logSelection(sel locationSelection, relaxed bool) {
	if len(sel.rejected) > 0 {
		items := make([]string, 0, len(sel.rejected))
		for _, r := range sel.rejected {
			items = append(items, fmt.Sprintf("%q reason=%q", r.location, r.reason))
		}
		discoveryLimitedSummaryf("SSDP locations rejected", "SSDP locations rejected count=%d items=%s", len(items), formatLogItems(items))
	}
	if len(sel.restricted) > 0 {
		action := "hidden"
		if relaxed {
			action = "eligible"
		}
		items := make([]string, 0, len(sel.restricted))
		for _, r := range sel.restricted {
			sources := make([]string, 0, len(r.sources))
			for _, src := range r.sources {
				sources = append(sources, src.String())
			}
			items = append(items, fmt.Sprintf("%q ip=%s from=%s renderer=%t reason=%q", r.location, r.addr, strings.Join(sources, ","), r.renderer, r.reason))
		}
		discoveryLimitedSummaryf("SSDP strict restriction", "SSDP strict restriction relaxed=%t action=%s count=%d selected=%d items=%s", relaxed, action, len(items), sel.relaxedSelected, formatLogItems(items))
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
