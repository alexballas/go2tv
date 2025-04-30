//go:build !windows
// +build !windows

package utils

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
)

var (
	ErrInvalidInput = errors.New("invalid ffmpeg input")
)

// ServeTranscodedStream passes an input file or io.Reader to ffmpeg and writes the output directly
// to our io.Writer.
func ServeTranscodedStream(w io.Writer, input any, ff *exec.Cmd, ffmpegPath, subs string, seekSeconds int) error {
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
	if err == nil {
		vf = fmt.Sprintf("subtitles='%s':charenc=%s,format=yuv420p", subs, charenc)
		if charenc == "UTF-8" {
			vf = fmt.Sprintf("subtitles='%s',format=yuv420p", subs)
		}
	}

	cmd := exec.Command(
		ffmpegPath,
		"-re",
		"-ss", strconv.Itoa(seekSeconds),
		"-copyts",
		"-i", in,
		"-vcodec", "h264",
		"-acodec", "aac",
		"-ac", "2",
		"-vf", vf,
		"-preset", "ultrafast",
		"-movflags", "+faststart",
		"-f", "mpegts",
		"pipe:1",
	)

	*ff = *cmd

	if in == "pipe:0" {
		ff.Stdin = input.(io.Reader)
	}

	ff.Stdout = w

	return ff.Run()
}
