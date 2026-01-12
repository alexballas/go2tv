//go:build !windows

package utils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
)

var (
	ErrInvalidInput = errors.New("invalid ffmpeg input")
)

// SubtitleSize represents the subtitle size option
type SubtitleSize int

const (
	SubtitleSizeSmall SubtitleSize = iota
	SubtitleSizeMedium
	SubtitleSizeLarge
)

// ServeTranscodedStream passes an input file or io.Reader to ffmpeg and writes the output directly
// to our io.Writer. The context is used to kill ffmpeg when the HTTP request is cancelled.
func ServeTranscodedStream(ctx context.Context, w io.Writer, input any, ff *exec.Cmd, ffmpegPath, subs string, seekSeconds int, subSize SubtitleSize) error {
	// Pipe streaming is not great as explained here
	// https://video.stackexchange.com/questions/34087/ffmpeg-fails-on-pipe-to-pipe-video-decoding.
	// That's why if we have the option to pass the file directly to ffmpeg, we should.
	var in string
	switch f := input.(type) {
	case string:
		in = f
	case io.Reader:
		in = "pipe:0"
	default:
		return ErrInvalidInput
	}

	if ff != nil && ff.Process != nil {
		_ = ff.Process.Kill()
	}

	vf := "format=yuv420p"
	charenc, err := getCharDet(subs)

	// For now I'm just using Medium as default.
	// We can later add an option in the GUI to select subtitle size.
	if err == nil {
		fontSize := 24
		switch subSize {
		case SubtitleSizeSmall:
			fontSize = 20
		case SubtitleSizeLarge:
			fontSize = 30
		}

		forceStyle := fmt.Sprintf(":force_style='FontSize=%d,Outline=1'", fontSize)

		if charenc == "UTF-8" {
			vf = fmt.Sprintf("subtitles='%s'%s,format=yuv420p", subs, forceStyle)
		} else {
			vf = fmt.Sprintf("subtitles='%s':charenc=%s%s,format=yuv420p", subs, charenc, forceStyle)
		}
	}

	vf = "scale='min(1920,iw)':'min(1080,ih)':force_original_aspect_ratio=decrease,scale=trunc(iw/2)*2:trunc(ih/2)*2," + vf

	// Build ffmpeg arguments
	// For piped input, skip -ss parameter entirely (even -ss 0) as it can cause issues
	args := []string{ffmpegPath, "-re"}

	if in != "pipe:0" && seekSeconds > 0 {
		args = append(args, "-ss", strconv.Itoa(seekSeconds), "-copyts")
	}

	args = append(args,
		"-i", in,
		"-vcodec", "h264",
		"-acodec", "aac",
		"-ac", "2",
		"-vf", vf,
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-g", "30",
		"-keyint_min", "15",
		"-sc_threshold", "0",
		"-movflags", "+faststart",
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-max_delay", "0",
		"-f", "mpegts",
		"pipe:1",
	)

	// Use regular Command instead of CommandContext to avoid nil pointer crash
	// when context cancels before process starts
	cmd := exec.Command(args[0], args[1:]...)

	*ff = *cmd

	if in == "pipe:0" {
		ff.Stdin = input.(io.Reader)
	}

	ff.Stdout = w

	// Start the process first
	if err := ff.Start(); err != nil {
		return err
	}

	// Now handle context cancellation in a goroutine (process is guaranteed to be non-nil)
	done := make(chan error, 1)
	go func() {
		done <- ff.Wait()
	}()

	select {
	case <-ctx.Done():
		// Context cancelled, kill the process
		if ff.Process != nil {
			_ = ff.Process.Kill()
		}
		<-done // Wait for process to exit
		return ctx.Err()
	case err := <-done:
		return err
	}
}
