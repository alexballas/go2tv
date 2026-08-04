package utils

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// escapeFFmpegPath escapes special characters in paths for FFmpeg filtergraph syntax.
// FFmpeg filtergraph requires escaping: \ ' : [ ]
func escapeFFmpegPath(path string) string {
	// Order matters: escape backslashes first
	path = strings.ReplaceAll(path, "\\", "\\\\")
	path = strings.ReplaceAll(path, "'", "'\\''")
	path = strings.ReplaceAll(path, ":", "\\:")
	path = strings.ReplaceAll(path, "[", "\\[")
	path = strings.ReplaceAll(path, "]", "\\]")
	return path
}

// ServeChromecastTranscodedStream transcodes media to Chromecast-compatible format.
// Output: fragmented MP4 with H.264 video and AAC audio for HTTP streaming.
// The context is used to kill ffmpeg when the HTTP request is cancelled.
//
// Parameters:
//   - ctx: Context for cancellation (pass r.Context() from HTTP handler)
//   - w: HTTP response writer to stream transcoded output
//   - input: Media source - either string (filepath) or io.Reader
//   - ff: Pointer to exec.Cmd for FFmpeg process management (cleanup)
//   - opts: TranscodeOptions containing FFmpeg path, subtitles, seek position, and logger
func ServeChromecastTranscodedStream(
	ctx context.Context,
	w io.Writer,
	input any,
	ff *exec.Cmd,
	opts *TranscodeOptions,
) error {
	if opts == nil || opts.FFmpegPath == "" {
		return ErrInvalidInput
	}

	isRawInput := opts.RawInput != nil

	// Readers backed by a real file (e.g. Android content:// descriptors) are
	// handed to ffmpeg as a seekable fd rather than an unseekable pipe.
	if r, ok := input.(io.Reader); ok && !isRawInput {
		if f, ok := underlyingOSFile(r); ok {
			input = f
		}
	}

	var in string
	switch f := input.(type) {
	case string:
		in = f
	case *os.File:
		in = ffmpegInputForFile(opts.FFmpegPath, f)
	case io.Reader:
		in = "pipe:0"
	default:
		return ErrInvalidInput
	}

	if ff != nil && ff.Process != nil {
		if isRawInput && ff.ProcessState == nil {
			return ErrTranscodeBusy
		}
		_ = ff.Process.Kill()
	}

	// Build video filter chain.
	// Raw screencast input doesn't carry subtitle tracks.
	scaleFilter := "scale='min(1920,iw)':'min(1080,ih)':force_original_aspect_ratio=decrease,scale=trunc(iw/2)*2:trunc(ih/2)*2"
	subFilter := ""
	if !isRawInput {
		var err error
		subFilter, err = subtitleBurnFilter(opts.FFmpegPath, opts.SubsPath, opts.SubtitleSize)
		if err != nil && opts.LogOutput != nil {
			// Log error but continue without subtitles
			opts.LogError("ServeChromecastTranscodedStream", "subtitle burn-in skipped", err)
		}
	}

	profile := videoEncoderProfileChromecastFile
	if isRawInput {
		profile = videoEncoderProfileChromecastRaw
	}
	encoderPlan := selectTranscodeVideoEncoder(opts.FFmpegPath, profile)
	buildArgs := func(plan videoEncoderPlan) []string {
		vf := joinVideoFilters(subFilter, scaleFilter, plan.filterTail)

		// For piped input, skip -ss parameter entirely (even -ss 0) as it can cause issues.
		// File transcoding is deliberately unpaced so the renderer can build a
		// startup buffer instead of waiting on exactly real-time FFmpeg output.
		args := []string{opts.FFmpegPath}

		if in != "pipe:0" && opts.SeekSeconds > 0 {
			args = append(args, "-ss", strconv.Itoa(opts.SeekSeconds), "-copyts")
		}
		args = append(args, plan.globalArgs...)

		if isRawInput {
			pixelFormat := strings.ToLower(opts.RawInput.PixelFormat)
			if pixelFormat == "" {
				pixelFormat = "bgra"
			}
			frameRate := opts.RawInput.FrameRate
			if frameRate == 0 {
				frameRate = 60
			}
			args = append(
				args,
				"-f", "rawvideo",
				"-pix_fmt", pixelFormat,
				"-s", fmt.Sprintf("%dx%d", opts.RawInput.Width, opts.RawInput.Height),
				"-r", strconv.FormatUint(uint64(frameRate), 10),
			)
		}

		args = append(
			args,
			"-i", in,
			"-vf", vf,
		)
		args = append(args, plan.codecArgs...)

		if isRawInput {
			args = append(args, "-frag_duration", "250000")

			// Screen capture stream contains video only.
			args = append(args, "-an")
		} else {
			args = append(
				args,
				"-c:a", "aac",
				"-b:a", "192k",
				"-ar", "48000",
				"-ac", "2",
			)
		}

		args = append(
			args,
			"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
			"-f", "mp4",
			"pipe:1",
		)
		return args
	}

	if isRawInput && (opts.RawInput.Width == 0 || opts.RawInput.Height == 0) {
		return ErrInvalidInput
	}

	bytesWritten, err := runFFmpegTranscode(ctx, ff, input, in, w, buildArgs(encoderPlan))
	if err == nil {
		return nil
	}

	// If HW encoder fails before stream starts, retry file-based transcode with software for this request.
	if encoderPlan.hardware && in != "pipe:0" && bytesWritten == 0 && ctx.Err() == nil {
		software := transcodeSoftwareEncoderPlan(profile)
		_, swErr := runFFmpegTranscode(ctx, ff, input, in, w, buildArgs(software))
		if swErr == nil {
			return nil
		}
		return swErr
	}

	return err
}
