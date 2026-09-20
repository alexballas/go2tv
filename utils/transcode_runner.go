package utils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func runFFmpegTranscode(
	ctx context.Context,
	ff *exec.Cmd,
	input any,
	in string,
	w io.Writer,
	args []string,
) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	cmd := exec.Command(args[0], args[1:]...)
	setSysProcAttr(cmd)

	*ff = *cmd
	switch in {
	case "fd:":
		// The child reads fd 0 directly and shares its offset, so rewind to
		// keep retries (e.g. hardware -> software encoder) deterministic.
		f := input.(*os.File)
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return 0, fmt.Errorf("rewind ffmpeg input: %w", err)
		}
		ff.Stdin = f
	case "pipe:0":
		ff.Stdin = input.(io.Reader)
	}

	cw := &countingWriter{w: w}
	var stderr bytes.Buffer
	ff.Stdout = cw
	ff.Stderr = &stderr

	if err := ff.Start(); err != nil {
		return 0, fmt.Errorf("%w: %s", err, tailFFmpegStderr(strings.TrimSpace(stderr.String()), 240))
	}

	done := make(chan error, 1)
	go func() {
		done <- ff.Wait()
	}()

	select {
	case <-ctx.Done():
		if ff.Process != nil {
			_ = ff.Process.Kill()
		}
		<-done
		return cw.n, ctx.Err()
	case err := <-done:
		if err != nil {
			return cw.n, fmt.Errorf("%w: %s", err, tailFFmpegStderr(strings.TrimSpace(stderr.String()), 240))
		}
		return cw.n, nil
	}
}

// Only retry replayable input before any bytes reach the renderer. Even a
// container header makes a restart unsafe on the same HTTP response.
func runTranscodeWithFallback(
	ctx context.Context, ff *exec.Cmd, input any, in string, w io.Writer,
	plan videoEncoderPlan, profile videoEncoderProfile, hw string,
	buildArgs func(videoEncoderPlan, string) []string, logger *slog.Logger,
) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		decoder := hw
		if decoder == "" {
			decoder = "software"
		}
		args := buildArgs(plan, hw)
		if logger != nil {
			// This is the requested backend; FFmpeg may internally use software
			// for codecs unsupported by the selected hardware decoder.
			logger.DebugContext(ctx, "transcode attempt", "profile", profile, "requested_decoder", decoder, "encoder", plan.codec, "args", args)
		}
		written, err := runFFmpegTranscode(ctx, ff, input, in, w, args)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil || in == "pipe:0" || written != 0 {
			return err
		}
		failedEncoder := plan.codec
		var nextAction string
		switch {
		case hw != "":
			hw = ""
			nextAction = "software_decode"
		case plan.hardware:
			plan = transcodeSoftwareEncoderPlan(profile)
			nextAction = "software_encode"
		default:
			return err
		}
		if logger != nil {
			// These identify the failed attempt, not a proven faulty component.
			logger.WarnContext(ctx, "transcode startup fallback", "failed_decoder", decoder, "failed_encoder", failedEncoder, "next_action", nextAction, "next_encoder", plan.codec, "error", err)
		}
	}
}
