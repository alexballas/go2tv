package utils

import (
	"context"
	"os/exec"
	"time"
)

// Bounds a hung binary, not a slow one: a cold first run under antivirus or
// Gatekeeper scanning can take several seconds.
const ffmpegCheckTimeout = 30 * time.Second

func CheckFFmpeg(ffmpeg string) error {
	return CheckFFmpegContext(context.Background(), ffmpeg)
}

func CheckFFmpegContext(ctx context.Context, ffmpeg string) error {
	return checkFFmpeg(ctx, ffmpeg, ffmpegCheckTimeout)
}

func checkFFmpeg(ctx context.Context, ffmpeg string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
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
