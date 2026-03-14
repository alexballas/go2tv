package utils

import (
	"context"
	"fmt"
	"io"
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

	var in string
	isRawInput := false
	switch f := input.(type) {
	case string:
		in = f
	case io.Reader:
		in = "pipe:0"
		isRawInput = opts != nil && opts.RawInput != nil
	default:
		return ErrInvalidInput
	}

	if ff != nil && ff.Process != nil {
		if isRawInput && ff.ProcessState == nil {
			return ErrTranscodeBusy
		}
		_ = ff.Process.Kill()
	}

	// Build video filter chain
	baseFilter := "scale='min(1920,iw)':'min(1080,ih)':force_original_aspect_ratio=decrease,scale=trunc(iw/2)*2:trunc(ih/2)*2"

	// Add subtitle burning if configured.
	// Raw screencast input doesn't carry subtitle tracks.
	if !isRawInput && opts.SubsPath != "" {
		charenc, err := getCharDet(opts.SubsPath)
		if err != nil {
			// Log error but continue without subtitles
			if opts.LogOutput != nil {
				opts.LogError("ServeChromecastTranscodedStream", "getCharDet failed, continuing without subtitles", err)
			}
		} else {
			fontSize := 24 // Medium (default)
			switch opts.SubtitleSize {
			case SubtitleSizeSmall:
				fontSize = 20
			case SubtitleSizeLarge:
				fontSize = 30
			}

			forceStyle := fmt.Sprintf(":force_style='FontSize=%d,Outline=1'", fontSize)

			// Escape special characters for FFmpeg filtergraph syntax
			escapedPath := escapeFFmpegPath(opts.SubsPath)

			if charenc == "UTF-8" {
				baseFilter = fmt.Sprintf("subtitles='%s'%s,%s", escapedPath, forceStyle, baseFilter)
			} else {
				baseFilter = fmt.Sprintf("subtitles='%s':charenc=%s%s,%s", escapedPath, charenc, forceStyle, baseFilter)
			}
		}
	}

	profile := videoEncoderProfileChromecastFile
	if isRawInput {
		profile = videoEncoderProfileChromecastRaw
	}
	encoderPlan := selectTranscodeVideoEncoder(opts.FFmpegPath, profile)
	buildArgs := func(plan videoEncoderPlan) []string {
		vf := joinVideoFilters(baseFilter, plan.filterTail)

		// For piped input, skip -ss parameter entirely (even -ss 0) as it can cause issues
		// Also skip -re for piped input as it interacts badly with streams
		args := []string{opts.FFmpegPath}
		if in != "pipe:0" {
			args = append(args, "-re")
		}

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
			args = append(args,
				"-f", "rawvideo",
				"-pix_fmt", pixelFormat,
				"-s", fmt.Sprintf("%dx%d", opts.RawInput.Width, opts.RawInput.Height),
				"-r", strconv.FormatUint(uint64(frameRate), 10),
			)
		}

		args = append(args,
			"-i", in,
			"-vf", vf,
		)
		args = append(args, plan.codecArgs...)

		if isRawInput {
			args = append(args, "-frag_duration", "250000")

			// Screen capture stream contains video only.
			args = append(args, "-an")
		} else {
			args = append(args,
				"-c:a", "aac",
				"-b:a", "192k",
				"-ar", "48000",
				"-ac", "2",
			)
		}

		args = append(args,
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
