//go:build !windows

package processutil

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	configureParentDeath(cmd.SysProcAttr)
}

// Kill stops the command and descendants that inherited its process group.
func Kill(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid || cmd.SysProcAttr.Pgid != 0 {
		return cmd.Process.Kill()
	}
	// Signal the group even once Wait reaped the leader: an exited wrapper's
	// FFmpeg can still hold the pipes Wait is joining. The kernel never reuses
	// a PID while it names a live group, so this cannot hit another process
	// unless the group died and its ID was recycled; callers that know the
	// command already exited skip Kill.
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// Process groups need no per-command state.
func contain(*exec.Cmd) {}

// Release frees what Start attached to cmd; call it after Wait.
func Release(*exec.Cmd) {}
