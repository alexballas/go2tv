//go:build windows

package gui

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// configureManagedSysProcAttr hides the child console window. Only the
// hidden-window attributes are shared with the RTMP helper; forced cleanup
// goes through the Job Object below, never taskkill.
func configureManagedSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}

// managedContainment owns a non-inheritable Job Object with
// kill-on-job-close, so the child tree dies with the parent process even on
// parent crash.
type managedContainment struct {
	job windows.Handle
}

// containManagedProcess must run immediately after Start and before any
// handshake traffic; assignment failure means the run must be aborted.
func containManagedProcess(cmd *exec.Cmd) (*managedContainment, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	err = windows.AssignProcessToJobObject(job, handle)
	_ = windows.CloseHandle(handle)
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	return &managedContainment{job: job}, nil
}

// KillTree terminates every process in the Job.
func (c *managedContainment) KillTree() error {
	if c == nil || c.job == 0 {
		return nil
	}
	return windows.TerminateJobObject(c.job, 1)
}

func (c *managedContainment) Close() error {
	if c == nil || c.job == 0 {
		return nil
	}
	err := windows.CloseHandle(c.job)
	c.job = 0
	return err
}
