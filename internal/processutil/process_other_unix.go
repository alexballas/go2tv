//go:build !linux && !windows

package processutil

import "syscall"

func configureParentDeath(*syscall.SysProcAttr) {}
