package utils

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
)

var (
	ErrInvalidInput  = errors.New("invalid ffmpeg input")
	ErrTranscodeBusy = errors.New("transcode already running")
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
	// Readers backed by a real file (e.g. Android content:// descriptors) are
	// handed to ffmpeg as a seekable fd rather than an unseekable pipe.
	if r, ok := input.(io.Reader); ok {
		if f, ok := underlyingOSFile(r); ok {
			input = f
		}
	}

	var in string
	switch f := input.(type) {
	case string:
		in = f
	case *os.File:
		in = ffmpegInputForFile(ffmpegPath, f)
	case io.Reader:
		in = "pipe:0"
	default:
		return ErrInvalidInput
	}

	if ff != nil && ff.Process != nil {
		_ = ff.Process.Kill()
	}

	// Stream without subtitles when the filter can't be built.
	subFilter, _ := subtitleBurnFilter(ffmpegPath, subs, subSize)

	encoderPlan := selectTranscodeVideoEncoder(ffmpegPath, videoEncoderProfileDLNA)
	buildArgs := func(plan videoEncoderPlan) []string {
		vf := joinVideoFilters(
			"scale='min(1920,iw)':'min(1080,ih)':force_original_aspect_ratio=decrease",
			"scale=trunc(iw/2)*2:trunc(ih/2)*2",
			subFilter,
			plan.filterTail,
		)

		args := []string{ffmpegPath}
		args = append(args, plan.globalArgs...)

		if in != "pipe:0" && seekSeconds > 0 {
			args = append(args, "-ss", strconv.Itoa(seekSeconds), "-copyts")
		}

		args = append(
			args,
			"-i", in,
			"-vf", vf,
		)
		args = append(args, plan.codecArgs...)
		args = append(
			args,
			"-acodec", "aac",
			"-ac", "2",
			"-movflags", "+faststart",
			"-fflags", "nobuffer",
			"-flags", "low_delay",
			"-max_delay", "0",
		)
		args = append(args, "-f", "mpegts", "pipe:1")
		return args
	}

	bytesWritten, err := runFFmpegTranscode(ctx, ff, input, in, w, buildArgs(encoderPlan))
	if err == nil {
		return nil
	}

	// If HW encoder fails before streaming starts, retry once with software for this request.
	if encoderPlan.hardware && in != "pipe:0" && bytesWritten == 0 && ctx.Err() == nil {
		software := transcodeSoftwareEncoderPlan(videoEncoderProfileDLNA)
		_, swErr := runFFmpegTranscode(ctx, ff, input, in, w, buildArgs(software))
		if swErr == nil {
			return nil
		}
		return swErr
	}

	return err
}
