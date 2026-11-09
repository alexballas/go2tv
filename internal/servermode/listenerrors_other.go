//go:build !windows

package servermode

import "syscall"

var (
	errListenAddrInUse    error = syscall.EADDRINUSE
	errListenAddrNotAvail error = syscall.EADDRNOTAVAIL
	errListenAccess       error = syscall.EACCES
)
