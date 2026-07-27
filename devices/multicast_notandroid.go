//go:build !android

package devices

// Only Android filters multicast traffic behind a lock. Everywhere else the
// SSDP and mDNS scans reach the network unaided, so this compiles away.
func withMulticastGuard(scan func()) {
	scan()
}
