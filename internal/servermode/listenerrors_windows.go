package servermode

import "golang.org/x/sys/windows"

// Winsock reports bind failures as WSA* codes, which the POSIX-named
// syscall errnos never match on Windows.
var (
	errListenAddrInUse    error = windows.WSAEADDRINUSE
	errListenAddrNotAvail error = windows.WSAEADDRNOTAVAIL
	errListenAccess       error = windows.WSAEACCES
)
