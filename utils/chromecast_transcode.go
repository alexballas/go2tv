package utils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"go2tv.app/go2tv/v2/internal/mediasource"
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

// chromecastMinFragment (us): 8s validated on device; 4s still stalled.
const chromecastMinFragment = "8000000"

// ServeChromecastTranscodedStream transcodes media to Chromecast-compatible format.
// Output: fragmented MP4 with H.264 video and AAC audio for HTTP streaming.
// The context is used to kill ffmpeg when the HTTP request is cancelled.
//
// Parameters:
//   - ctx: Context for cancellation (pass r.Context() from HTTP handler)
//   - w: HTTP response writer to stream transcoded output
//   - input: Media source - either string (filepath) or io.Reader
//   - ff: fresh exec.Cmd per call; receives the running FFmpeg command
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
	if !isRawInput {
		if url := progressiveReaderURL(input); url != "" {
			input = url
		}
	}

	// Readers backed by a real file (e.g. Android content:// descriptors) are
	// handed to ffmpeg as a seekable fd rather than an unseekable pipe.
	if r, ok := input.(io.Reader); ok && !isRawInput {
		if f, ok := underlyingOSFile(r); ok {
			input = f
		}
	}

	var in string
	// fileInput: bounded media FFmpeg reads faster than real time.
	var fileInput bool
	switch f := input.(type) {
	case string:
		in = mediasource.Input(f)
		input = in
		fileInput = true
	case *os.File:
		in = ffmpegInputForFile(opts.FFmpegPath, f)
		// Seekable files still pipe on FFmpeg without the fd: protocol.
		_, err := f.Seek(0, io.SeekCurrent)
		fileInput = err == nil
	case io.Reader:
		in = "pipe:0"
	default:
		return ErrInvalidInput
	}

	// Build video filter chain.
	// Raw screencast input doesn't carry subtitle tracks.
	burn := &subtitleBurn{cleanup: func() {}}
	if !isRawInput {
		var err error
		burn, err = prepareSubtitleBurn(ctx, opts)
		defer burn.cleanup()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if err != nil && opts.LogOutput != nil {
			// Log error but continue without subtitles
			opts.LogError("ServeChromecastTranscodedStream", "subtitle burn-in skipped", err)
		}
	}

	profile := videoEncoderProfileChromecastFile
	if isRawInput {
		profile = videoEncoderProfileChromecastRaw
	}
	encoderPlan, err := selectTranscodeVideoEncoder(opts.FFmpegPath, profile)
	if err != nil {
		return err
	}
	buildArgs := func(plan videoEncoderPlan, hw string) []string {
		copyTS := (in != "pipe:0" && opts.SeekSeconds > 0) || burn.overlay != ""
		var vf string
		switch hw {
		case "cuda":
			vf = cudaTranscodeScaleFilter
		case "vaapi":
			vf = vaapiTranscodeScaleFilter
		default:
			vf = joinVideoFilters(burn.videoFilter(copyTS), softwareTranscodeScaleFilter, plan.filterTail)
		}

		// For piped input, skip -ss parameter entirely (even -ss 0) as it can cause issues.
		// File transcoding is deliberately unpaced so the renderer can build a
		// startup buffer instead of waiting on exactly real-time FFmpeg output.
		args := []string{opts.FFmpegPath}

		if in != "pipe:0" && opts.SeekSeconds > 0 {
			args = append(args, "-ss", strconv.Itoa(opts.SeekSeconds))
		}
		if copyTS {
			args = append(args, "-copyts")
		}
		args = append(args, transcodeInputArgs(plan, hw)...)

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

		args = append(args, "-i", in)
		if burn.overlay != "" || burn.bitmap != nil {
			args = append(args, burn.videoArgs(softwareTranscodeScaleFilter, plan.filterTail, opts.AudioTrack)...)
		} else {
			args = append(args, "-vf", vf)
			args = append(args, selectedStreamMaps(opts.AudioTrack)...)
		}
		args = append(args, plan.codecArgs...)

		if isRawInput {
			args = append(args, "-frag_duration", "250000")

			// Screen capture stream contains video only.
			args = append(args, "-an")
		} else {
			switch {
			case opts.TorrentSource != nil:
				// Flush fragments without waiting for the next keyframe, which may
				// otherwise require downloading the entire low-frame-rate file.
				// Exactly 8s, not 1s: short fragments stall the receiver (below).
				args = append(args, "-frag_duration", chromecastMinFragment, "-min_frag_duration", chromecastMinFragment)
			case fileInput && plan.codec != "libx264":
				// Chromecast intermittently stalls for good at the first fragment
				// boundary when it falls within ~5s of the start (1s GOP: stuck at
				// 3.1s, any receiver). Keep 1s keyframes, cut fragments >= 8s.
				// libx264 here keeps its long default GOP, which never stalled;
				// a minimum would delay its first fragment to the second keyframe.
				args = append(args, "-min_frag_duration", chromecastMinFragment)
			}
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
		return append(args, burn.outputArgs()...)
	}

	if isRawInput && (opts.RawInput.Width == 0 || opts.RawInput.Height == 0) {
		return ErrInvalidInput
	}

	hw := selectTranscodeVideoDecoder(opts.FFmpegPath, encoderPlan, burn.enabled(), in, isRawInput)
	return runTranscodeWithFallback(ctx, ff, input, in, w, encoderPlan, profile, hw, buildArgs, opts.Log())
}
