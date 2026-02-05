//go:build !windows

package utils

import "os/exec"

func setSysProcAttr(_ *exec.Cmd) {}
