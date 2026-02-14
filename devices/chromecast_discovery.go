package devices

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
)

const (
	// CapabilityVideoOut is the bitmask for video output capability (bit 0)
	CapabilityVideoOut = 1
	// mDNS query timeout per request
	chromecastQueryTimeout = 750 * time.Millisecond
	// Faster polling while cache is empty for quick first discovery
	chromecastPollIntervalFast = 1 * time.Second
	// Slower polling once at least one device is known to reduce network load
	chromecastPollIntervalSlow = 4 * time.Second
	// Interface refresh cadence for add/remove changes
	chromecastIfaceRefreshInterval = 20 * time.Second
)

var (
	// chromeCastDevices caches discovered Chromecast devices
	// map key: "host:port" address, value: castDevice struct
	chromeCastDevices = make(map[string]castDevice)
	ccMu              sync.Mutex
	ccWarmupOnce      sync.Once
)

type castDevice struct {
	Name        string
	IsAudioOnly bool
}

func upsertChromecastFromMDNSEntry(entry *mdns.ServiceEntry) {
	if entry == nil || entry.AddrV4 == nil {
		return
	}
	if !strings.Contains(entry.Name, "_googlecast") {
		return
	}

	address := fmt.Sprintf("%s:%d", entry.AddrV4, entry.Port)
	friendlyName := entry.Name

	for _, txt := range entry.InfoFields {
		if after, ok := strings.CutPrefix(txt, "fn="); ok {
			friendlyName = after
			break
		}
	}

	if idx := strings.Index(friendlyName, "._googlecast"); idx > 0 {
		friendlyName = friendlyName[:idx]
	}

	isAudioOnly := false
	for _, txt := range entry.InfoFields {
		if after, ok := strings.CutPrefix(txt, "ca="); ok {
			isAudioOnly = isChromecastAudioOnly(after)
			break
		}
	}

	ccMu.Lock()
	chromeCastDevices[address] = castDevice{
		Name:        friendlyName,
		IsAudioOnly: isAudioOnly,
	}
	ccMu.Unlock()
}

func warmupChromecastCache(timeout time.Duration) {
	interfaces := getActiveNetworkInterfaces()

	entriesCh := make(chan *mdns.ServiceEntry, 256)
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		for entry := range entriesCh {
			upsertChromecastFromMDNSEntry(entry)
		}
	}()

	queryIface := func(iface *net.Interface) {
		params := mdns.DefaultParams("_googlecast._tcp")
		params.Entries = entriesCh
		params.Timeout = timeout
		params.DisableIPv6 = true
		params.WantUnicastResponse = true
		params.Logger = log.New(io.Discard, "", 0)
		params.Interface = iface
		_ = mdns.Query(params)
	}

	if len(interfaces) > 0 {
		var wg sync.WaitGroup
		for _, iface := range interfaces {
			wg.Add(1)
			go func(iface net.Interface) {
				defer wg.Done()
				queryIface(&iface)
			}(iface)
		}
		wg.Wait()
	} else {
		queryIface(nil)
	}

	close(entriesCh)
	<-doneCh
}

func currentChromecastPollInterval() time.Duration {
	ccMu.Lock()
	hasDevices := len(chromeCastDevices) > 0
	ccMu.Unlock()
	if hasDevices {
		return chromecastPollIntervalSlow
	}
	return chromecastPollIntervalFast
}

// StartChromecastDiscoveryLoop continuously discovers Chromecast devices on the local network using mDNS.
// It runs indefinitely until the provided context is canceled, using adaptive polling.
// Discovered devices are stored in a global map with their network addresses as keys.
// The function runs background goroutines to handle device discovery and health checking.
func StartChromecastDiscoveryLoop(ctx context.Context) {
	go discoverChromecastDevices(ctx)
	go healthCheckChromecastDevices(ctx)
}

// discoverChromecastDevices continuously browses for Chromecast devices using mDNS.
// It queries on all active network interfaces to handle Windows systems with multiple
// adapters (VPN, Hyper-V, Docker, etc.) where the OS default interface may not be
// the one connected to the Chromecast network.
func discoverChromecastDevices(ctx context.Context) {
	const googlecastService = "_googlecast._tcp"

	startPollingWorker := func(parent context.Context, iface *net.Interface) (context.CancelFunc, error) {
		entriesCh := make(chan *mdns.ServiceEntry, 256)
		workerCtx, cancel := context.WithCancel(parent)

		go func() {
			for {
				select {
				case <-workerCtx.Done():
					return
				case entry := <-entriesCh:
					upsertChromecastFromMDNSEntry(entry)
				}
			}
		}()

		go func() {
			pollTimer := time.NewTimer(0)
			defer pollTimer.Stop()

			for {
				select {
				case <-workerCtx.Done():
					return
				case <-pollTimer.C:
				}

				params := mdns.DefaultParams(googlecastService)
				params.Entries = entriesCh
				params.Timeout = chromecastQueryTimeout
				params.DisableIPv6 = true
				params.WantUnicastResponse = true
				params.Logger = log.New(io.Discard, "", 0)
				if iface != nil {
					params.Interface = iface
				}
				_ = mdns.Query(params)

				pollTimer.Reset(currentChromecastPollInterval())
			}
		}()

		return cancel, nil
	}

	type worker struct {
		cancel context.CancelFunc
	}

	pollWorkers := make(map[int]worker)
	refresh := func() bool {
		interfaces := getActiveNetworkInterfaces()

		active := make(map[int]net.Interface, len(interfaces))
		for _, iface := range interfaces {
			active[iface.Index] = iface

			if _, ok := pollWorkers[iface.Index]; ok {
				continue
			}

			pollIface := iface
			cancel, err := startPollingWorker(ctx, &pollIface)
			if err == nil {
				pollWorkers[iface.Index] = worker{cancel: cancel}
			}
		}

		for idx, w := range pollWorkers {
			if idx == -1 {
				continue
			}
			if _, ok := active[idx]; !ok {
				w.cancel()
				delete(pollWorkers, idx)
			}
		}

		if len(interfaces) == 0 {
			if _, ok := pollWorkers[-1]; ok {
				return true
			}
			cancel, err := startPollingWorker(ctx, nil)
			if err == nil {
				pollWorkers[-1] = worker{cancel: cancel}
			}
		} else if w, ok := pollWorkers[-1]; ok {
			w.cancel()
			delete(pollWorkers, -1)
		}

		return len(pollWorkers) > 0
	}

	ccWarmupOnce.Do(func() {
		warmupChromecastCache(chromecastQueryTimeout)
	})

	_ = refresh()

	refreshTicker := time.NewTicker(chromecastIfaceRefreshInterval)
	defer refreshTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			for _, w := range pollWorkers {
				w.cancel()
			}
			return
		case <-refreshTicker.C:
			_ = refresh()
		}
	}
}

// getActiveNetworkInterfaces returns all network interfaces that are up,
// multicast-capable, not loopback, and have an IPv4 address. This is used to query mDNS on
// all possible interfaces where Chromecast devices might be reachable.
func getActiveNetworkInterfaces() []net.Interface {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var active []net.Interface
	for _, iface := range interfaces {
		// Skip down, loopback, or non-multicast interfaces.
		if iface.Flags&net.FlagUp == 0 ||
			iface.Flags&net.FlagLoopback != 0 ||
			iface.Flags&net.FlagMulticast == 0 {
			continue
		}

		// Check if interface has an IPv4 address
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		hasIPv4 := false
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ipnet.IP.To4() != nil && !ipnet.IP.IsLoopback() {
					hasIPv4 = true
					break
				}
			}
		}

		if hasIPv4 {
			active = append(active, iface)
		}
	}

	return active
}

// healthCheckChromecastDevices periodically checks if cached Chromecast devices are still alive
// and removes stale devices from the cache
func healthCheckChromecastDevices(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ccMu.Lock()
			for address := range chromeCastDevices {
				if !HostPortIsAlive(address) {
					delete(chromeCastDevices, address)
				}
			}
			ccMu.Unlock()
		}
	}
}

// GetChromecastDevices returns the current cached Chromecast devices.
// Returns a slice of Device structs with type set to DeviceTypeChromecast.
func GetChromecastDevices() []Device {
	ccMu.Lock()
	cacheEmpty := len(chromeCastDevices) == 0
	ccMu.Unlock()
	if cacheEmpty {
		ccWarmupOnce.Do(func() {
			warmupChromecastCache(chromecastQueryTimeout)
		})
	}

	ccMu.Lock()
	defer ccMu.Unlock()

	result := make([]Device, 0, len(chromeCastDevices))
	for address, device := range chromeCastDevices {
		// Convert to URL format to match DLNA (GUI expects URLs)
		addressURL := "http://" + address
		// Add suffix to distinguish Chromecast devices in the UI
		friendlyName := device.Name + " (Chromecast)"
		if device.IsAudioOnly {
			friendlyName = device.Name + " (Chromecast Audio)"
		}

		result = append(result, Device{
			Name:        friendlyName,
			Addr:        addressURL,
			Type:        DeviceTypeChromecast,
			IsAudioOnly: device.IsAudioOnly,
		})
	}

	return result
}

// HostPortIsAlive checks if a device at the given address is reachable via TCP connection.
// Returns true if the connection succeeds within 2 seconds.
func HostPortIsAlive(address string) bool {
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// isChromecastAudioOnly checks if a device is audio-only based on the "ca" capability field.
// The "ca" field in mDNS TXT records is a bitmask where bit 0 (value 1) indicates Video Out support.
// If bit 0 is NOT set, the device is considered audio-only (e.g. Chromecast Audio, Google Home speakers).
// Returns true if audio-only, false if it supports video or if parsing fails.
func isChromecastAudioOnly(caField string) bool {
	ca, err := strconv.Atoi(caField)
	if err != nil {
		// If parsing fails, we default to false (assume it's a standard video device)
		// to avoid restricting functionality unnecessarily.
		return false
	}
	return (ca & CapabilityVideoOut) == 0
}
