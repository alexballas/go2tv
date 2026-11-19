package devices

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alexballas/go-ssdp"
	"github.com/pkg/errors"
	"go2tv.app/go2tv/v2/soapcalls"
)

const (
	DeviceTypeDLNA       = "DLNA"
	DeviceTypeChromecast = "Chromecast"
	dlnaDiscoveryDelay   = 2
	dlnaDiscoveryPause   = time.Second
	dlnaLocationTimeout  = 1500 * time.Millisecond
	ssdpFirstPort        = 3339
	ssdpPortCount        = 100
)

type Device struct {
	ID          string
	Name        string
	Addr        string
	Type        string
	IsAudioOnly bool
	// RequiresRelaxedDiscovery marks a renderer discovered from a restricted SSDP location.
	RequiresRelaxedDiscovery bool
}

var ErrNoDeviceAvailable = errors.New("loadSSDPservices: No available Media Renderers")

var (
	ssdpSearch            = ssdp.Search
	loadDiscoveredDevices = soapcalls.LoadDevicesFromPinnedLocation
	discoveryLogOutput    io.Writer
	discoveryLogMu        sync.Mutex
	discoverySummaryState = make(map[string]summaryState)
	dlnaDevices           []Device
	dlnaMu                sync.RWMutex
	discoveryStartOnce    sync.Once
)

type summaryState struct {
	lastLine string
	repeats  int
	lastAt   time.Time
	skipped  int
}

// discoveryLimitedInterval bounds how often a changed line whose content remote
// hosts control is logged.
const discoveryLimitedInterval = time.Minute

var discoveryNow = time.Now

// SetDiscoveryLogOutput enables lightweight discovery debug logs.
func SetDiscoveryLogOutput(w io.Writer) {
	discoveryLogMu.Lock()
	discoveryLogOutput = w
	clear(discoverySummaryState)
	discoveryLogMu.Unlock()
}

func discoveryDebugf(format string, args ...any) {
	discoveryLogMu.Lock()
	defer discoveryLogMu.Unlock()

	if discoveryLogOutput == nil {
		return
	}

	_, _ = fmt.Fprintf(discoveryLogOutput, "discovery: "+format+"\n", args...)
}

func discoverySummaryf(key string, format string, args ...any) {
	discoveryLogMu.Lock()
	defer discoveryLogMu.Unlock()

	if discoveryLogOutput == nil {
		return
	}

	line := fmt.Sprintf(format, args...)
	state := discoverySummaryState[key]
	if state.lastLine == line {
		state.repeats++
		discoverySummaryState[key] = state
		return
	}

	if state.repeats > 0 {
		_, _ = fmt.Fprintf(discoveryLogOutput, "discovery: %s repeated=%d\n", key, state.repeats)
	}

	discoverySummaryState[key] = summaryState{lastLine: line}
	_, _ = fmt.Fprintf(discoveryLogOutput, "discovery: %s\n", line)
}

// discoveryLimitedSummaryf is discoverySummaryf for lines whose content remote
// hosts control: identical lines are de-duplicated, and a changed line is
// logged at most once per discoveryLimitedInterval, counting the changes
// skipped in between.
func discoveryLimitedSummaryf(key string, format string, args ...any) {
	discoveryLogMu.Lock()
	defer discoveryLogMu.Unlock()

	if discoveryLogOutput == nil {
		return
	}

	line := fmt.Sprintf(format, args...)
	state := discoverySummaryState[key]
	if state.lastLine == line {
		state.repeats++
		discoverySummaryState[key] = state
		return
	}

	now := discoveryNow()
	if !state.lastAt.IsZero() && now.Sub(state.lastAt) < discoveryLimitedInterval {
		state.skipped++
		discoverySummaryState[key] = state
		return
	}

	if state.repeats > 0 {
		_, _ = fmt.Fprintf(discoveryLogOutput, "discovery: %s repeated=%d\n", key, state.repeats)
	}
	printed := line
	if state.skipped > 0 {
		printed += fmt.Sprintf(" skipped_changes=%d", state.skipped)
	}

	// Keep the unsuffixed line so an unchanged line still de-duplicates.
	discoverySummaryState[key] = summaryState{lastLine: line, lastAt: now}
	_, _ = fmt.Fprintf(discoveryLogOutput, "discovery: %s\n", printed)
}

func formatSummaryList(values []string, maxItems int) string {
	if len(values) == 0 {
		return ""
	}

	items := slices.Clone(values)
	slices.Sort(items)
	items = slices.Compact(items)

	if maxItems <= 0 || len(items) <= maxItems {
		return strings.Join(items, ",")
	}

	return fmt.Sprintf(
		"%s,+%d more",
		strings.Join(items[:maxItems], ","),
		len(items)-maxItems,
	)
}

func deviceNames(devices []Device, maxItems int) string {
	names := make([]string, 0, len(devices))
	for _, device := range devices {
		names = append(names, device.Name)
	}

	return formatSummaryList(names, maxItems)
}

func locationHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}

	return u.Host
}

// IsChromecastURL returns true if the URL points to a Chromecast device (port 8009).
func IsChromecastURL(deviceURL string) bool {
	u, err := url.Parse(deviceURL)
	if err != nil {
		return false
	}
	return u.Port() == "8009"
}

// LoadSSDPservices returns a slice of DLNA devices that support
// required playback services.
func LoadSSDPservices(delay int) ([]Device, error) {
	return LoadSSDPservicesContext(context.Background(), delay)
}

// LoadSSDPservicesContext discovers DLNA renderers and bounds description loads by ctx.
func LoadSSDPservicesContext(ctx context.Context, delay int) ([]Device, error) {
	if ctx == nil {
		return nil, errors.New("LoadSSDPservicesContext: context required")
	}
	var loadErrors, filteredDevices, unnamedDevices, dupNameCount int
	loadErrorHosts := make([]string, 0)

	type searchResult struct {
		list []ssdp.Service
		addr string
		err  error
	}
	search := make(chan searchResult, 1)
	go func() {
		list, addr, err := searchSSDP(ctx, delay)
		search <- searchResult{list: list, addr: addr, err: err}
	}()
	var scan searchResult
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case scan = <-search:
	}
	if scan.err != nil {
		discoveryDebugf("LoadSSDPservices search error: %v", scan.err)
		return nil, fmt.Errorf("LoadSSDPservices search error: %w", scan.err)
	}
	list, addrString := scan.list, scan.addr

	// Collect unique locations that pass the discovery policy (a single location
	// may have multiple embedded devices). ST values only order candidates:
	// some vendors reply with non-AVTransport ST values while still exposing
	// AVTransport in LOCATION XML.
	relaxed := RelaxedDiscovery()
	sel := selectLocations(list, relaxed)
	logSelection(sel, relaxed)
	setHiddenSSDPLocations(sel.hidden)
	noticeHidden(len(sel.hidden))

	var (
		allDevices []Device
		resultsMu  sync.Mutex
		wg         sync.WaitGroup
		sem        = make(chan struct{}, 10)
	)

	for _, d := range sel.selected {
		loc := d.location
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()

			locCtx, cancel := context.WithTimeout(ctx, dlnaLocationTimeout)
			devices, err := loadDiscoveredDevices(locCtx, loc, d.addr)
			cancel()

			resultsMu.Lock()
			defer resultsMu.Unlock()

			if err != nil {
				loadErrors++
				loadErrorHosts = append(loadErrorHosts, locationHost(loc))
				return
			}

			for _, dev := range devices {
				if !isDLNADeviceCastable(dev) {
					filteredDevices++
					continue
				}

				name := sanitizeDeviceName(dev.FriendlyName)
				if strings.TrimSpace(dev.FriendlyName) == "" {
					unnamedDevices++
				}
				allDevices = append(allDevices, Device{
					Name:                     name,
					Addr:                     loc,
					Type:                     DeviceTypeDLNA,
					RequiresRelaxedDiscovery: d.restricted,
				})
			}
		})
	}
	wg.Wait()

	// Handle duplicate names
	deviceList := make(map[string]Device)
	dupNames := make(map[string]int)
	for _, dev := range allDevices {
		fn := dev.Name
		_, exists := dupNames[fn]
		dupNames[fn]++
		if exists {
			fn = fn + " (" + dev.Addr + ")"
		}

		deviceList[fn] = dev
	}

	for fn, c := range dupNames {
		if c > 1 {
			dupNameCount++
			dev := deviceList[fn]
			delete(deviceList, fn)
			fn = fn + " (" + dev.Addr + ")"
			deviceList[fn] = dev
		}
	}

	if len(deviceList) == 0 {
		discoverySummaryf(
			"LoadSSDPservices summary",
			"LoadSSDPservices summary listen_addr=%q ssdp_results=%d unique_locations=%d relaxed=%t rejected=%d restricted=%d hidden=%d relaxed_selected=%d capped=%d load_errors=%d error_hosts=%q filtered=%d duplicate_names=%d unnamed=%d returned=0 devices=%q",
			addrString,
			len(list),
			sel.seen,
			relaxed,
			len(sel.rejected),
			len(sel.restricted),
			len(sel.hidden),
			sel.relaxedSelected,
			sel.capped,
			loadErrors,
			formatSummaryList(loadErrorHosts, 3),
			filteredDevices,
			dupNameCount,
			unnamedDevices,
			"",
		)
		return nil, ErrNoDeviceAvailable
	}

	// Convert map to Device slice with proper type
	result := make([]Device, 0, len(deviceList))
	for name, dev := range deviceList {
		dev.Name = name
		result = append(result, dev)
	}
	discoverySummaryf(
		"LoadSSDPservices summary",
		"LoadSSDPservices summary listen_addr=%q ssdp_results=%d unique_locations=%d relaxed=%t rejected=%d restricted=%d hidden=%d relaxed_selected=%d capped=%d load_errors=%d error_hosts=%q filtered=%d duplicate_names=%d unnamed=%d returned=%d devices=%q",
		addrString,
		len(list),
		sel.seen,
		relaxed,
		len(sel.rejected),
		len(sel.restricted),
		len(sel.hidden),
		sel.relaxedSelected,
		sel.capped,
		loadErrors,
		formatSummaryList(loadErrorHosts, 3),
		filteredDevices,
		dupNameCount,
		unnamedDevices,
		len(result),
		deviceNames(result, 3),
	)

	return result, nil
}

// StartDiscovery starts the background discovery loops once for the process.
func StartDiscovery(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	discoveryStartOnce.Do(func() {
		discoveryDebugf("Discovery loops starting")
		StartChromecastDiscoveryLoop(ctx)
		startDLNADiscoveryLoop(ctx)
	})
}

func startDLNADiscoveryLoop(ctx context.Context) {
	go func() {
		pollTimer := time.NewTimer(dlnaDiscoveryPause)
		defer pollTimer.Stop()

		if ctx.Err() != nil {
			return
		}
		// Initial immediate discovery before the first timer tick
		refreshDLNADevices(1)

		for {
			select {
			case <-ctx.Done():
				return
			case <-pollTimer.C:
			}

			refreshDLNADevices(dlnaDiscoveryDelay)
			pollTimer.Reset(dlnaDiscoveryPause)
		}
	}()
}

func refreshDLNADevices(delay int) {
	defer feed.notifyDLNAScan()

	var (
		devices []Device
		err     error
	)
	withMulticastGuard(func() {
		devices, err = LoadSSDPservices(delay)
	})
	if err != nil {
		if stderrors.Is(err, ErrNoDeviceAvailable) {
			setDLNADevices(nil)
			return
		}

		discoveryDebugf("DLNA discovery scan error: %v", err)
		return
	}

	setDLNADevices(devices)
}

func setDLNADevices(devices []Device) {
	dlnaMu.Lock()
	dlnaDevices = slices.Clone(devices)
	dlnaMu.Unlock()
	feed.publish()
}

func getDLNADevices() []Device {
	dlnaMu.RLock()
	result := slices.Clone(dlnaDevices)
	dlnaMu.RUnlock()
	if !RelaxedDiscovery() {
		result = slices.DeleteFunc(result, func(d Device) bool {
			return d.RequiresRelaxedDiscovery
		})
	}
	return result
}

func isDLNADeviceCastable(dev *soapcalls.DMRextracted) bool {
	if dev == nil {
		return false
	}

	return dev.AvtransportControlURL != ""
}

// LoadAllDevices returns the latest cached DLNA and Chromecast devices.
func LoadAllDevices() ([]Device, error) {
	dlna := getDLNADevices()
	chromecast := getChromecastDevicesSnapshot()

	combined := make([]Device, 0, len(dlna)+len(chromecast))
	combined = append(combined, dlna...)
	combined = append(combined, chromecast...)

	if len(combined) == 0 {
		discoverySummaryf(
			"LoadAllDevices summary",
			"LoadAllDevices summary cached=true dlna=%d dlna_names=%q chromecast=%d chromecast_names=%q combined=0",
			len(dlna),
			deviceNames(dlna, 3),
			len(chromecast),
			deviceNames(chromecast, 3),
		)
		return nil, ErrNoDeviceAvailable
	}

	sortDevices(combined)
	discoverySummaryf(
		"LoadAllDevices summary",
		"LoadAllDevices summary cached=true dlna=%d dlna_names=%q chromecast=%d chromecast_names=%q combined=%d combined_names=%q",
		len(dlna),
		deviceNames(dlna, 3),
		len(chromecast),
		deviceNames(chromecast, 3),
		len(combined),
		deviceNames(combined, 4),
	)

	return combined, nil
}

// ScanAllDevices performs one bounded fresh DLNA/mDNS scan.
func ScanAllDevices(ctx context.Context, dlnaDelay int) ([]Device, error) {
	if ctx == nil {
		return nil, errors.New("ScanAllDevices: context required")
	}
	if dlnaDelay <= 0 {
		dlnaDelay = 1
	}
	type dlnaResult struct {
		devices []Device
		err     error
	}
	dlnaCh := make(chan dlnaResult, 1)
	go func() {
		found, err := LoadSSDPservicesContext(ctx, dlnaDelay)
		dlnaCh <- dlnaResult{devices: found, err: err}
	}()
	warmupChromecastCacheContext(ctx, chromecastQueryTimeout)
	result := <-dlnaCh
	chromecast := getChromecastDevicesSnapshot()
	combined := append(slices.Clone(result.devices), chromecast...)
	if len(combined) == 0 {
		if result.err != nil && !stderrors.Is(result.err, ErrNoDeviceAvailable) {
			return nil, result.err
		}
		return nil, ErrNoDeviceAvailable
	}
	sortDevices(combined)
	return combined, nil
}

// sortDevices sorts devices by type (DLNA first, then Chromecast)
// and alphabetically within each type
func sortDevices(devices []Device) {
	sort.Slice(devices, func(i, j int) bool {
		// If types differ, DLNA comes first
		if devices[i].Type != devices[j].Type {
			return devices[i].Type < devices[j].Type
		}
		// Within same type, sort alphabetically by name
		return devices[i].Name < devices[j].Name
	})
}

// Try each port with the discovery socket itself. An interface-specific
// probe can succeed even when the wildcard address is already in use.
func searchSSDP(ctx context.Context, delay int) ([]ssdp.Service, string, error) {
	for offset := range ssdpPortCount {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		addr := fmt.Sprintf("0.0.0.0:%d", ssdpFirstPort+offset)
		list, err := ssdpSearch(ssdp.All, delay, addr)
		var listenErr *net.OpError
		if !stderrors.As(err, &listenErr) || listenErr.Op != "listen" {
			return list, addr, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	const ephemeral = "0.0.0.0:0"
	list, err := ssdpSearch(ssdp.All, delay, ephemeral)
	return list, ephemeral, err
}
