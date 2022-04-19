package utils

import (
	"errors"
	"io"
	"os/exec"
	"syscall"
)

// ServeTranscodedStream passes an input file or io.Reader to ffmpeg and writes the output directly
// to the http.ResponseWriter.
func ServeTranscodedStream(w io.Writer, input interface{}, ff *exec.Cmd) error {
	// Pipe streaming is not great as explained here
	// https://video.stackexchange.com/questions/34087/ffmpeg-fails-on-pipe-to-pipe-video-decoding.
	// That's why if we have the option to pass the file directly to ffmpeg, then we should go with
	// that option.
	var in string
	switch f := input.(type) {
	case string:
		in = f
	case io.Reader:
		in = "pipe:0"
	default:
		return errors.New("invalid ffmpeg input")
	}

	if ff != nil && ff.Process != nil {
		_ = ff.Process.Kill()
	}

	cmd := exec.Command(
		"ffmpeg",
		"-re",
		"-i", in,
		"-vcodec", "h264",
		"-acodec", "aac",
		"-ac", "2",
		"-vf", "format=yuv420p",
		"-movflags", "+faststart",
		"-f", "flv",
		"pipe:1",
	)

	ff = cmd

	// Hide the command window when running ffmpeg. (Windows specific)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}

	if in == "pipe:0" {
		ff.Stdin = input.(io.Reader)
	}

	ff.Stdout = w

	return ff.Run()
}
