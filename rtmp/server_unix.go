//go:build !windows

package rtmp

import (
	"os/exec"
	"time"
)

func setSysProcAttr(cmd *exec.Cmd) {
	// No additional attributes needed for Unix
}

func killProcess(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = terminatePID(cmd.Process.Pid, 2*time.Second)
	}
}
