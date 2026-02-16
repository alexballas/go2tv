//go:build !windows

package rtmp

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

func terminatePID(pid int, grace time.Duration) error {
	if pid <= 0 {
		return nil
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("sigterm pid %d: %w", pid, err)
	}

	if waitPIDExit(pid, grace) {
		return nil
	}

	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("sigkill pid %d: %w", pid, err)
	}

	if waitPIDExit(pid, 1*time.Second) {
		return nil
	}

	return fmt.Errorf("pid %d still alive", pid)
}

func waitPIDExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !pidAlive(pid)
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}

	// EPERM means it exists but we can't signal it.
	if errors.Is(err, syscall.EPERM) {
		return true
	}

	return false
}
