//go:build !windows && !android

package utils

import "os/exec"

func setSysProcAttr(_ *exec.Cmd) {}
