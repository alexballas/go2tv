//go:build !windows

package utils

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
)

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

	// Build video filter chain
	vf := "scale='min(1920,iw)':'min(1080,ih)':force_original_aspect_ratio=decrease,scale=trunc(iw/2)*2:trunc(ih/2)*2"

	// Add subtitle burning if configured
	if opts.SubsPath != "" {
		charenc, err := getCharDet(opts.SubsPath)
		if err == nil {
			fontSize := 24 // Medium (default)
			switch opts.SubtitleSize {
			case SubtitleSizeSmall:
				fontSize = 20
			case SubtitleSizeLarge:
				fontSize = 30
			}

			forceStyle := fmt.Sprintf(":force_style='FontSize=%d,Outline=1'", fontSize)

			if charenc == "UTF-8" {
				vf = fmt.Sprintf("subtitles='%s'%s,%s", opts.SubsPath, forceStyle, vf)
			} else {
				vf = fmt.Sprintf("subtitles='%s':charenc=%s%s,%s", opts.SubsPath, charenc, forceStyle, vf)
			}
		}
	}

	// Append format conversion
	vf = vf + ",format=yuv420p"

	cmd := exec.CommandContext(ctx,
		opts.FFmpegPath,
		"-re",
		"-ss", strconv.Itoa(opts.SeekSeconds),
		"-copyts",
		"-i", in,
		"-c:v", "libx264",
		"-profile:v", "high",
		"-level", "4.1",
		"-preset", "fast",
		"-crf", "23",
		"-maxrate", "10M",
		"-bufsize", "20M",
		"-vf", vf,
		"-c:a", "aac",
		"-b:a", "192k",
		"-ar", "48000",
		"-ac", "2",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4",
		"pipe:1",
	)

	*ff = *cmd

	if in == "pipe:0" {
		ff.Stdin = input.(io.Reader)
	}

	ff.Stdout = w

	return ff.Run()
}
