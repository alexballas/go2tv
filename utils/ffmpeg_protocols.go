package utils

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var ffmpegProtocolCache sync.Map

// ffmpegInputForFile returns the ffmpeg input URL for media handed over as an
// open file descriptor. ffmpeg's pipe: protocol never seeks, even when fd 0 is
// a regular file, which breaks demuxing of e.g. MP4s with a trailing moov
// atom. The fd: protocol does seek, so prefer it and only fall back to pipe:0
// when the file isn't seekable or the binary lacks the protocol.
func ffmpegInputForFile(ffmpegPath string, f *os.File) string {
	if _, err := f.Seek(0, io.SeekCurrent); err != nil {
		return "pipe:0"
	}
	if !ffmpegInputProtocolAvailable(ffmpegPath, "fd") {
		return "pipe:0"
	}

	return "fd:"
}

// underlyingOSFile returns the *os.File backing r, unwrapping reader wrappers
// (e.g. refyne's URI readers) that expose their inner reader via Unwrap.
func underlyingOSFile(r io.Reader) (*os.File, bool) {
	for r != nil {
		switch v := r.(type) {
		case *os.File:
			return v, true
		case interface{ Unwrap() io.ReadSeekCloser }:
			r = v.Unwrap()
		default:
			return nil, false
		}
	}

	return nil, false
}

func ffmpegInputProtocolAvailable(ffmpegPath, name string) bool {
	key := ffmpegPath + "|" + name
	if cached, ok := ffmpegProtocolCache.Load(key); ok {
		return cached.(bool)
	}

	protocols, err := ffmpegInputProtocolSet(ffmpegPath)
	if err != nil {
		return false
	}

	_, ok := protocols[name]
	ffmpegProtocolCache.Store(key, ok)
	return ok
}

func ffmpegInputProtocolSet(ffmpegPath string) (map[string]struct{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), transcodeEncoderProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-protocols")
	setSysProcAttr(cmd)

	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("ffmpeg -protocols timeout after %s", transcodeEncoderProbeTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("ffmpeg -protocols failed: %w", err)
	}

	protocols := make(map[string]struct{})
	inInputSection := false
	for line := range strings.SplitSeq(string(out), "\n") {
		switch trimmed := strings.TrimSpace(line); {
		case strings.HasPrefix(trimmed, "Input:"):
			inInputSection = true
		case strings.HasPrefix(trimmed, "Output:"):
			inInputSection = false
		case inInputSection && trimmed != "":
			protocols[trimmed] = struct{}{}
		}
	}

	return protocols, nil
}
