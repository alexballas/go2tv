//go:build !windows

package utils

import (
	"errors"
	"syscall"
)

// isAddrInUse reports a taken port.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
