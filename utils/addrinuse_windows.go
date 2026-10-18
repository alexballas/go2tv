//go:build windows

package utils

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isAddrInUse reports a taken port. Windows returns WSAEADDRINUSE, which
// syscall.EADDRINUSE doesn't match.
func isAddrInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE)
}
