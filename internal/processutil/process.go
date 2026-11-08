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

// Start starts a long-lived command. On Windows it also binds the process
// tree to a Job Object, so Kill reaches a wrapper's FFmpeg after the wrapper
// exits and the tree dies with go2tv. Call Release after Wait.
func Start(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	contain(cmd)
	return nil
}

// Run is Start, Wait and Release.
func Run(cmd *exec.Cmd) error {
	if err := Start(cmd); err != nil {
		return err
	}
	defer Release(cmd)
	return cmd.Wait()
}
