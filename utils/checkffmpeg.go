package utils

import "os/exec"

func CheckFFmpeg(ffmpeg string) error {
	checkffmpeg := exec.Command(ffmpeg, "-h")
	setSysProcAttr(checkffmpeg)
	_, err := checkffmpeg.Output()
	if err != nil {
		return err
	}
	return nil
}
