package utils

import (
	"os/exec"
	"syscall"
)

func CheckFFmpeg(ffmpeg string) error {
	checkffmpeg := exec.Command(ffmpeg, "-h")
	checkffmpeg.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}

	_, err := checkffmpeg.Output()
	if err != nil {
		return err
	}
	return nil
}
