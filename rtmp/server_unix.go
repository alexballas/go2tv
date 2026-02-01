//go:build !windows

package rtmp

import "os/exec"

func setSysProcAttr(cmd *exec.Cmd) {
	// No additional attributes needed for Unix
}

func killProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
