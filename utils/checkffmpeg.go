package utils

import "os/exec"

func CheckFFmpeg(ffmpeg string) error {
	resolvedFFmpeg, err := ResolveFFmpegPath(ffmpeg)
	if err != nil {
		return err
	}

	checkffmpeg := exec.Command(resolvedFFmpeg, "-h")
	setSysProcAttr(checkffmpeg)
	_, err = checkffmpeg.Output()
	if err != nil {
		return err
	}
	return nil
}
