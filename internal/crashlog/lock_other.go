//go:build !windows

package crashlog

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockPending takes an flock that survives closing f: it belongs to the open
// file description the runtime's duplicate descriptor still shares, and the
// kernel drops it when the process exits.
func lockPending(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

// pendingInUse reports whether a running process holds path's lock.
func pendingInUse(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		return false
	}
	return errors.Is(err, unix.EWOULDBLOCK)
}
