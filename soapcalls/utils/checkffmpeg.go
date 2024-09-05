//go:build !windows
// +build !windows

package utils

import "os/exec"

func CheckFFmpeg(ffmpeg string) error {
	checkffmpeg := exec.Command(ffmpeg, "-h")
	_, err := checkffmpeg.Output()
	if err != nil {
		return err
	}
	return nil
}
