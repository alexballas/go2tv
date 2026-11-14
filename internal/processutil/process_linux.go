//go:build linux

package processutil

import "syscall"

func configureParentDeath(attr *syscall.SysProcAttr) { attr.Pdeathsig = syscall.SIGKILL }
