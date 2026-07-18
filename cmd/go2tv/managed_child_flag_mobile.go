//go:build android || ios

package main

// Mobile builds register no -managed-child flag: GUI-managed server children
// are a desktop-only feature and server mode is unsupported on mobile.
