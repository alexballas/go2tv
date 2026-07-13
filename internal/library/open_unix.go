//go:build unix

package library

import (
	"os"
	"syscall"
)

func openRootFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
