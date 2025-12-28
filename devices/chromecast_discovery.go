package devices

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

var (
	// chromeCastDevices caches discovered Chromecast devices
	// map key: "host:port" address, value: friendly device name
	chromeCastDevices = make(map[string]string)
	ccMu              sync.Mutex
)

// StartChromecastDiscoveryLoop continuously discovers Chromecast devices on the local network using mDNS.
// It runs indefinitely until the provided context is canceled, searching for devices every 10 seconds.
// Discovered devices are stored in a global map with their network addresses as keys.
// The function runs background goroutines to handle device discovery and health checking.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
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

		timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)

		resolver, err := zeroconf.NewResolver(nil)
		if err != nil {
			<-timeoutCtx.Done()
			cancel()
			continue
		}

		entries := make(chan *zeroconf.ServiceEntry)
		go func(results <-chan *zeroconf.ServiceEntry) {
			for entry := range results {
				if len(entry.AddrIPv4) == 0 {
					continue
				}

				address := fmt.Sprintf("%s:%d", entry.AddrIPv4[0], entry.Port)
				friendlyName := entry.Instance

				for _, v := range entry.Text {
					if strings.Contains(v, "fn=") {
						friendlyName = strings.TrimPrefix(v, "fn=")
					}
				}

				ccMu.Lock()
				chromeCastDevices[address] = friendlyName
				ccMu.Unlock()
			}
		}(entries)

		err = resolver.Browse(timeoutCtx, "_googlecast._tcp", "local", entries)
		if err != nil {
			<-timeoutCtx.Done()
			cancel()
			continue
		}

		<-timeoutCtx.Done()
		cancel()
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
