//go:build android || ios

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

// StartChromecastDiscoveryLoop attempts mDNS discovery on mobile platforms.
// Note: This may not work without native NSD/Bonjour integration.
func StartChromecastDiscoveryLoop(ctx context.Context) {
	go discoverChromecastDevices(ctx)
	go healthCheckChromecastDevices(ctx)
}

// discoverChromecastDevices continuously browses for Chromecast devices using mDNS
func discoverChromecastDevices(ctx context.Context) {
	// Suppress hashicorp/mdns logging
	log.SetFlags(0)

	// Loop to handle query restarts (long-running)
	for {
		// Checks if context is done before starting new query
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Create channel for results - MUST be new for each Query call
		// as mdns closes the channel when finished.
		entriesCh := make(chan *mdns.ServiceEntry, 10)

		// Start a goroutine to process results for this query
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

		params := mdns.DefaultParams("_googlecast._tcp")
		params.Entries = entriesCh
		// Set a long timeout to keep the client open/listening.
		params.Timeout = 1 * time.Hour
		params.DisableIPv6 = true
		params.Logger = log.New(io.Discard, "", 0)

		// Use QueryContext to respect context cancellation
		// This will close entriesCh when it returns
		_ = mdns.QueryContext(ctx, params)

		// If we are here, either timeout occurred or context cancelled.
		select {
		case <-ctx.Done():
			return
		default:
			// If just timeout, loop again immediately
		}
	}
}

// healthCheckChromecastDevices periodically checks if cached Chromecast devices are still alive
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
func GetChromecastDevices() []Device {
	ccMu.Lock()
	defer ccMu.Unlock()

	result := make([]Device, 0, len(chromeCastDevices))
	for address, name := range chromeCastDevices {
		addressURL := "http://" + address
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
func HostPortIsAlive(address string) bool {
	conn, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
