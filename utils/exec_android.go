//go:build android

package utils

import (
	"os"
	"syscall"
	_ "unsafe" // for go:linkname
)

// Android < 12 traps pidfd_open with SIGSYS. Go's checkPidfd temporarily
// ignores that signal, but c-shared builds do not install its SIGSYS handler.
// Disable the probe before the GUI starts FFmpeg; ordinary process waiting
// and signaling remain available. See go2tv#181 and golang/go#69065.
// The Android packager already passes -checklinkname=0 for anet.
//
//go:linkname checkPidfdOnce os.checkPidfdOnce
var checkPidfdOnce func() error

func init() {
	checkPidfdOnce = func() error {
		return os.NewSyscallError("pidfd_open", syscall.ENOSYS)
	}
}
