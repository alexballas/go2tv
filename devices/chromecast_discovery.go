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
)

var (
	// chromeCastDevices caches discovered Chromecast devices
	// map key: "host:port" address, value: castDevice struct
	chromeCastDevices = make(map[string]castDevice)
	ccMu              sync.Mutex
)

type castDevice struct {
	Name        string
	IsAudioOnly bool
}

// StartChromecastDiscoveryLoop continuously discovers Chromecast devices on the local network using mDNS.
// It runs indefinitely until the provided context is canceled, searching for devices every 2 seconds.
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
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Get all active network interfaces
		interfaces := getActiveNetworkInterfaces()

		// Create channel for results (sized for multiple interfaces)
		entriesCh := make(chan *mdns.ServiceEntry, 10*len(interfaces)+10)

		// Start a goroutine to process results
		go func() {
			for entry := range entriesCh {
				if entry.AddrV4 == nil {
					continue
				}

				// Verify this is actually a Chromecast (filter out other services)
				if !strings.Contains(entry.Name, "_googlecast") {
					continue
				}

				address := fmt.Sprintf("%s:%d", entry.AddrV4, entry.Port)
				friendlyName := entry.Name

				// Parse TXT records for friendly name (fn=)
				for _, txt := range entry.InfoFields {
					if strings.HasPrefix(txt, "fn=") {
						friendlyName = strings.TrimPrefix(txt, "fn=")
						break
					}
				}

				// Clean up the name (remove service suffix if present)
				if idx := strings.Index(friendlyName, "._googlecast"); idx > 0 {
					friendlyName = friendlyName[:idx]
				}

				// Check if device is audio-only
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
		}()

		// Query on each interface to handle multi-interface Windows systems
		if len(interfaces) > 0 {
			var wg sync.WaitGroup
			for _, iface := range interfaces {
				wg.Add(1)
				go func(iface net.Interface) {
					defer wg.Done()
					params := mdns.DefaultParams("_googlecast._tcp")
					params.Entries = entriesCh
					params.Timeout = 2 * time.Second
					params.DisableIPv6 = true
					params.Logger = log.New(io.Discard, "", 0)
					params.Interface = &iface
					_ = mdns.Query(params)
				}(iface)
			}
			wg.Wait()
		} else {
			// Fallback: no specific interfaces found, use OS default
			params := mdns.DefaultParams("_googlecast._tcp")
			params.Entries = entriesCh
			params.Timeout = 2 * time.Second
			params.DisableIPv6 = true
			params.Logger = log.New(io.Discard, "", 0)
			_ = mdns.Query(params)
		}
		close(entriesCh)

		// Small delay before next scan
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// getActiveNetworkInterfaces returns all network interfaces that are up,
// not loopback, and have an IPv4 address. This is used to query mDNS on
// all possible interfaces where Chromecast devices might be reachable.
func getActiveNetworkInterfaces() []net.Interface {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var active []net.Interface
	for _, iface := range interfaces {
		// Skip down or loopback interfaces
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
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
