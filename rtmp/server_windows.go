//go:build windows

package rtmp

import (
	"fmt"
	"os/exec"
	"syscall"
)

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

func killProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		// On Windows, use taskkill to kill the entire process tree (/T)
		// and force termination (/F).
		tk := exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid))
		setSysProcAttr(tk)
		_ = tk.Run()
	}
}
