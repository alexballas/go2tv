package utils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"go2tv.app/go2tv/v2/internal/mediasource"
)

// AudioTranscodeMediaType matches the ADTS stream emitted by the native AAC
// encoder, available in bundled mobile FFmpeg builds too.
const AudioTranscodeMediaType = "audio/aac"

const audioHLSProbeTimeout = 2 * time.Second

// RemoteAudio is a station URL whose ICY titles are reported while relaying.
type RemoteAudio struct {
	URL     string
	OnTitle func(string)
}

// ServeAudioTranscodedStream converts a station/file to stereo AAC without
// video filters or subtitles. URL inputs use a Go HTTP proxy for TLS and HLS segments.
func ServeAudioTranscodedStream(ctx context.Context, w io.Writer, input any, ff *exec.Cmd, opts *TranscodeOptions) error {
	if opts == nil || opts.FFmpegPath == "" || ff == nil {
		return ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var in string
	var onTitle func(string)
	switch source := input.(type) {
	case RemoteAudio:
		in = mediasource.Input(source.URL)
		input, onTitle = in, source.OnTitle
	case string:
		in = mediasource.Input(source)
		input = in
	case *os.File:
		in = ffmpegInputForFile(opts.FFmpegPath, source)
	case io.Reader:
		in = "pipe:0"
	default:
		return ErrInvalidInput
	}
	var hls bool
	if strings.HasPrefix(in, "http://") || strings.HasPrefix(in, "https://") {
		proxy, inputURL, err := newAudioStreamProxy(ctx, in, onTitle)
		if err != nil {
			return err
		}
		defer proxy.close()
		in = inputURL
		hls, err = proxy.inputIsHLS(ctx, inputURL)
		if err != nil {
			return err
		}
	}
	if ff.Process != nil {
		_ = ff.Process.Kill()
	}
	args := []string{opts.FFmpegPath, "-hide_banner", "-loglevel", "error"}
	if hls {
		hlsOptions, err := audioHLSDemuxerOptions(ctx, opts.FFmpegPath)
		if err != nil {
			return err
		}
		args = append(args, hlsOptions...)
		args = append(args, "-f", "hls")
	}
	if strings.HasPrefix(in, "http://") || strings.HasPrefix(in, "https://") {
		args = append(args, "-protocol_whitelist", "http,tcp,crypto,data", "-rw_timeout", "15000000", "-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5")
	}
	if in != "pipe:0" && opts.SeekSeconds > 0 {
		args = append(args, "-ss", strconv.Itoa(opts.SeekSeconds))
	}
	args = append(args, "-i", in, "-map", "0:a:0", "-vn", "-sn", "-dn", "-c:a", "aac", "-profile:a", "aac_low", "-b:a", "192k", "-ar", "48000", "-ac", "2", "-f", "adts", "-flush_packets", "1", "pipe:1")
	opts.Log().DebugContext(ctx, "audio transcode", "args", args)
	_, err := runFFmpegTranscode(ctx, ff, input, in, w, args)
	return err
}

// Signed HTTP proxy routes restrict upstream access. Let HLS probe the media
// instead of rejecting script/opaque segment names. Query supported options
// so older desktop FFmpeg builds keep working too.
func audioHLSDemuxerOptions(ctx context.Context, ffmpegPath string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, audioHLSProbeTimeout)
	defer cancel()
	help, err := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-h", "demuxer=hls").Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("audio HLS options: %w", err)
	}
	var options []string
	for _, option := range []struct{ name, value string }{
		{"allowed_extensions", "ALL"},
		{"allowed_segment_extensions", "ALL"},
		{"extension_picky", "0"},
	} {
		if bytes.Contains(help, []byte("-"+option.name+" ")) {
			options = append(options, "-"+option.name, option.value)
		}
	}
	return options, nil
}
