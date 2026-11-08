//go:build !android

package utils

import (
	"os/exec"

	"go2tv.app/go2tv/v2/internal/processutil"
)

func setSysProcAttr(cmd *exec.Cmd) { processutil.Configure(cmd) }
