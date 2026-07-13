//go:build unix

package library

import (
	"net"
	"syscall"
	"testing"
)

func makeFIFO(path string) error {
	return syscall.Mkfifo(path, 0o600)
}

func makeSocket(t *testing.T, path string) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
}
