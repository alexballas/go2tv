// Package processutil owns cancellation of media subprocesses.
package processutil

import "os/exec"

// Configure must run before Start. Cancel the whole process group when a
// configured FFmpeg executable is a wrapper script. No WaitDelay: it would
// drop buffered stdout still owed to a slow renderer after a normal exit.
func Configure(cmd *exec.Cmd) {
	configure(cmd)
	if cmd.Cancel != nil {
		cmd.Cancel = func() error { return Kill(cmd) }
	}
}
