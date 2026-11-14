package main

import "C"

import (
	"fmt"
	"os/exec"

	_ "go2tv.app/go2tv/v2/utils"
)

//export Run
func Run() C.int {
	tt := []struct {
		script, output string
		code           int
	}{
		{"printf go2tv-pidfd-ok", "go2tv-pidfd-ok", 0},
		{"printf go2tv-pidfd-exit; exit 7", "go2tv-pidfd-exit", 7},
	}
	for _, tc := range tt {
		cmd := exec.Command("/system/bin/sh", "-c", tc.script)
		out, err := cmd.CombinedOutput()
		if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.code || string(out) != tc.output {
			fmt.Printf("subprocess %q: %v, state %v, output %q\n", tc.script, err, cmd.ProcessState, out)
			return 1
		}
	}
	return 0
}

func main() {}
