package utils

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"

	"go2tv.app/go2tv/v2/internal/mediasource"
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
// An optional logger records pipeline attempts and startup fallback reasons.
func ServeTranscodedStream(ctx context.Context, w io.Writer, input any, ff *exec.Cmd, ffmpegPath, subs string, seekSeconds int, subSize SubtitleSize, loggers ...*slog.Logger) error {
	var logger *slog.Logger
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return serveDLNATranscodedStream(ctx, w, input, ff, &TranscodeOptions{FFmpegPath: ffmpegPath, SubsPath: subs, SeekSeconds: seekSeconds, SubtitleSize: subSize}, logger)
}

// ServeDLNATranscodedStream also supports automatic embedded torrent captions.
func ServeDLNATranscodedStream(ctx context.Context, w io.Writer, input any, ff *exec.Cmd, opts *TranscodeOptions) error {
	if opts == nil || opts.FFmpegPath == "" {
		return ErrInvalidInput
	}
	return serveDLNATranscodedStream(ctx, w, input, ff, opts, opts.Log())
}

func serveDLNATranscodedStream(ctx context.Context, w io.Writer, input any, ff *exec.Cmd, opts *TranscodeOptions, logger *slog.Logger) error {
	ffmpegPath, seekSeconds := opts.FFmpegPath, opts.SeekSeconds
	if url := progressiveReaderURL(input); url != "" {
		input = url
	}
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
		in = mediasource.Input(f)
		input = in
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

	burn, err := prepareSubtitleBurn(ctx, opts)
	defer burn.cleanup()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if logger != nil {
			logger.WarnContext(ctx, "subtitle burn-in skipped", "error", err)
		}
	}

	encoderPlan := selectTranscodeVideoEncoder(ffmpegPath, videoEncoderProfileDLNA)
	buildArgs := func(plan videoEncoderPlan, hw string) []string {
		copyTS := (in != "pipe:0" && seekSeconds > 0) || burn.overlay != ""
		var vf string
		args := []string{ffmpegPath}
		switch hw {
		case "cuda":
			vf = cudaTranscodeScaleFilter
		case "vaapi":
			vf = vaapiTranscodeScaleFilter
		default:
			vf = joinVideoFilters(
				softwareTranscodeScaleFilter,
				burn.videoFilter(copyTS),
				plan.filterTail,
			)
		}
		args = append(args, transcodeInputArgs(plan, hw)...)

		if in != "pipe:0" && seekSeconds > 0 {
			args = append(args, "-ss", strconv.Itoa(seekSeconds))
		}
		if copyTS {
			args = append(args, "-copyts")
		}

		args = append(args, "-i", in)
		if burn.overlay != "" || burn.bitmap != nil {
			args = append(args, burn.videoArgs(softwareTranscodeScaleFilter, plan.filterTail)...)
		} else {
			args = append(args, "-vf", vf)
		}
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

	hw := selectTranscodeVideoDecoder(ffmpegPath, encoderPlan, burn.enabled(), in, false)
	return runTranscodeWithFallback(ctx, ff, input, in, w, encoderPlan, videoEncoderProfileDLNA, hw, buildArgs, logger)
}
