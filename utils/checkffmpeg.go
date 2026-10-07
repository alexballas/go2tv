package utils

import (
	"context"
	"os/exec"
	"time"
)

func CheckFFmpeg(ffmpeg string) error {
	return CheckFFmpegContext(context.Background(), ffmpeg)
}

func CheckFFmpegContext(ctx context.Context, ffmpeg string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resolvedFFmpeg, err := ResolveFFmpegPath(ffmpeg)
	if err != nil {
		return err
	}

	checkffmpeg := exec.CommandContext(ctx, resolvedFFmpeg, "-h")
	setSysProcAttr(checkffmpeg)
	checkffmpeg.WaitDelay = time.Second
	_, err = checkffmpeg.Output()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	return nil
}
