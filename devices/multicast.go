//go:build android

package devices

import "sync"

// Android Wi-Fi drops multicast and broadcast frames that are not addressed to
// the device unless a multicast lock is held, which is exactly the traffic SSDP
// and mDNS discovery is built on.
//
// The lock is taken around each scan rather than held for the life of the
// process: it stops the Wi-Fi chip filtering in hardware, so holding it while
// nothing is listening costs power for no benefit.
//
// Taking it needs refyne's driver, and this package stays clear of the GUI
// toolkit because cmd/go2tv-lite imports it, so the caller registers the hook.
var (
	multicastMu      sync.RWMutex
	multicastAcquire func()
	multicastRelease func()
)

// SetMulticastGuard registers the functions called around each discovery scan.
// Both must be non-nil to take effect, and acquire/release calls are expected to
// nest. Passing nil for either clears a previously registered guard.
func SetMulticastGuard(acquire, release func()) {
	multicastMu.Lock()
	defer multicastMu.Unlock()

	if acquire == nil || release == nil {
		multicastAcquire, multicastRelease = nil, nil
		return
	}
	multicastAcquire, multicastRelease = acquire, release
}

// withMulticastGuard runs scan with multicast traffic allowed through, if the
// platform needs and supports that. The release is paired with the acquire that
// was read at entry, so a guard replaced mid-scan cannot leave a lock held.
func withMulticastGuard(scan func()) {
	multicastMu.RLock()
	acquire, release := multicastAcquire, multicastRelease
	multicastMu.RUnlock()

	if acquire == nil {
		scan()
		return
	}

	acquire()
	defer release()
	scan()
}
