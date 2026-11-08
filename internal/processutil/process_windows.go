//go:build windows

package processutil

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	jobsMu sync.Mutex
	jobs   = map[*exec.Cmd]windows.Handle{}
)

func configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}

// contain is best effort: without a job, Kill falls back to taskkill.
func contain(cmd *exec.Cmd) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	// Before Wait, the open process handle keeps this PID from being reused.
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	err = windows.AssignProcessToJobObject(job, process)
	_ = windows.CloseHandle(process)
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	jobsMu.Lock()
	jobs[cmd] = job
	jobsMu.Unlock()
}

// Release closes cmd's job; kill-on-close reaps any leftover descendants.
func Release(cmd *exec.Cmd) {
	jobsMu.Lock()
	job, ok := jobs[cmd]
	delete(jobs, cmd)
	jobsMu.Unlock()
	if ok {
		_ = windows.CloseHandle(job)
	}
}

// Kill stops the command and its process tree.
func Kill(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	jobsMu.Lock()
	job, ok := jobs[cmd]
	if ok {
		// The job outlives an exited wrapper, so this still reaches its FFmpeg.
		err := windows.TerminateJobObject(job, 1)
		jobsMu.Unlock()
		return err
	}
	jobsMu.Unlock()
	// After Wait the PID may name an unrelated process tree. Before Wait, the
	// open process handle keeps the PID from being reused.
	if err := cmd.Process.Signal(syscall.Signal(0)); errors.Is(err, os.ErrProcessDone) {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tree := exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	configure(tree)
	if err := tree.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
