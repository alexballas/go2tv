//go:build !windows && !(android || ios)

package gui

import (
	"os/exec"
	"syscall"
)

// configureManagedSysProcAttr places the managed child in its own process
// group so a forced stop can kill the whole tree (child plus any FFmpeg
// grandchildren) with one signal.
func configureManagedSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// managedContainment tracks the child process group. Unix containment after a
// parent crash is best-effort only: the group kill below needs a live parent,
// so a crashed parent relies on stdin EOF alone and a hung grandchild can
// leak. That residual risk is accepted by the plan.
type managedContainment struct {
	pgid int
}

// containManagedProcess must run immediately after Start.
func containManagedProcess(cmd *exec.Cmd) (*managedContainment, error) {
	return &managedContainment{pgid: cmd.Process.Pid}, nil
}

// KillTree force-kills the child's whole process group.
func (c *managedContainment) KillTree() error {
	if c == nil || c.pgid <= 0 {
		return nil
	}
	return syscall.Kill(-c.pgid, syscall.SIGKILL)
}

func (c *managedContainment) Close() error { return nil }
