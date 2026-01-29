//go:build !(android || ios)

package devices

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
)

var (
	// chromeCastDevices caches discovered Chromecast devices
	// map key: "host:port" address, value: friendly device name
	chromeCastDevices = make(map[string]string)
	ccMu              sync.Mutex
)

// StartChromecastDiscoveryLoop continuously discovers Chromecast devices on the local network using mDNS.
// It runs indefinitely until the provided context is canceled, searching for devices every 2 seconds.
// Discovered devices are stored in a global map with their network addresses as keys.
// The function runs background goroutines to handle device discovery and health checking.
func StartChromecastDiscoveryLoop(ctx context.Context) {
	go discoverChromecastDevices(ctx)
	go healthCheckChromecastDevices(ctx)
}

// discoverChromecastDevices continuously browses for Chromecast devices using mDNS
func discoverChromecastDevices(ctx context.Context) {

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Create channel for results
		entriesCh := make(chan *mdns.ServiceEntry, 10)

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

				ccMu.Lock()
				chromeCastDevices[address] = friendlyName
				ccMu.Unlock()
			}
		}()

		// Lookup Chromecast devices (blocking call with timeout)
		params := mdns.DefaultParams("_googlecast._tcp")
		params.Entries = entriesCh
		params.Timeout = 2 * time.Second
		params.DisableIPv6 = true
		params.Logger = log.New(io.Discard, "", 0)

		_ = mdns.Query(params)
		close(entriesCh)

		// Small delay before next scan
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
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
	for address, name := range chromeCastDevices {
		// Convert to URL format to match DLNA (GUI expects URLs)
		addressURL := "http://" + address
		// Add suffix to distinguish Chromecast devices in the UI
		friendlyName := name + " (Chromecast)"
		result = append(result, Device{
			Name: friendlyName,
			Addr: addressURL,
			Type: DeviceTypeChromecast,
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
