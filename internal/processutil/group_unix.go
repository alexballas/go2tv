//go:build !windows

package processutil

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// KillGroupTree includes media commands that created their own process groups.
// Freeze the managed parent first so it cannot create children during teardown.
func KillGroupTree(pgid int) error {
	if pgid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pgid, syscall.SIGSTOP); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	defer syscall.Kill(-pgid, syscall.SIGKILL)
	owned := map[int]bool{pgid: true}
	groups := map[int]bool{pgid: true}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		output, err := exec.CommandContext(ctx, "ps", "-ax", "-o", "pid=,ppid=,pgid=").Output()
		if err != nil {
			// Still terminate every group already found, including the parent.
			for group := range groups {
				_ = syscall.Kill(-group, syscall.SIGKILL)
			}
			return fmt.Errorf("list managed descendants: %w", err)
		}
		added := false
		for line := range strings.SplitSeq(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 3 {
				continue
			}
			pid, _ := strconv.Atoi(fields[0])
			parent, _ := strconv.Atoi(fields[1])
			group, _ := strconv.Atoi(fields[2])
			if pid <= 0 || group <= 0 || owned[pid] || !owned[parent] {
				continue
			}
			if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
				continue // Child already exited; never signal a reused PID later.
			}
			owned[pid], groups[group], added = true, true, true
		}
		if !added {
			break
		}
	}
	var result error
	for group := range groups {
		if err := syscall.Kill(-group, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			result = errors.Join(result, err)
		}
	}
	return result
}
