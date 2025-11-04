package utils

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"syscall"
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
// to our io.Writer.
func ServeTranscodedStream(w io.Writer, input any, ff *exec.Cmd, ffmpegPath, subs string, seekSeconds int, subSize SubtitleSize) error {
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
		fontSize := 24 // Medium (default)
		switch subSize {
		case SubtitleSizeSmall:
			fontSize = 20
		case SubtitleSizeLarge:
			fontSize = 32
		}

		forceStyle := fmt.Sprintf(":force_style='FontSize=%d,Outline=2'", fontSize)

		if charenc == "UTF-8" {
			vf = fmt.Sprintf("subtitles='%s'%s,format=yuv420p", subs, forceStyle)
		} else {
			vf = fmt.Sprintf("subtitles='%s':charenc=%s%s,format=yuv420p", subs, charenc, forceStyle)
		}
	}

	vf = "scale='min(1920,iw)':'min(1080,ih)':force_original_aspect_ratio=decrease,scale=trunc(iw/2)*2:trunc(ih/2)*2," + vf

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
		"-tune", "zerolatency", // Reduces buffering
		"-g", "30", // Smaller GOP size for faster start
		"-keyint_min", "15",
		"-sc_threshold", "0", // Disable scene detection
		"-movflags", "+faststart",
		"-fflags", "nobuffer", // Reduce input buffering
		"-flags", "low_delay", // Low delay mode
		"-max_delay", "0", // Minimize muxer delay
		"-f", "mpegts",
		"pipe:1",
	)

	*ff = *cmd

	// Hide the command window when running ffmpeg. (Windows specific)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}

	if in == "pipe:0" {
		ff.Stdin = input.(io.Reader)
	}

	ff.Stdout = w

	return ff.Run()
}
