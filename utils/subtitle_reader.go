package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// GetSubsReaderContext discovers captions in provider-backed mobile media.
func GetSubsReaderContext(ctx context.Context, ffmpeg string, media io.ReadSeekCloser) ([]string, error) {
	var tracks []string
	err := withSubtitleReader(ctx, ffmpeg, media, func(ctx context.Context, input string, stdin io.Reader) error {
		probe, err := ResolveFFprobePath(ffmpeg)
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, probe, "-v", "error", "-select_streams", "s", "-show_streams", "-of", "json", input)
		setSysProcAttr(cmd)
		cmd.WaitDelay, cmd.Stdin = time.Second, stdin
		output, err := cmd.Output()
		if err != nil {
			return err
		}
		var info ffprobeInfoforSubs
		if err := json.Unmarshal(output, &info); err != nil {
			return err
		}
		tracks, err = subtitleNames(info.Streams)
		if err == nil && len(tracks) == 0 {
			return ErrNoSubs
		}
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("GetSubsReaderContext: %w", err)
	}
	return tracks, nil
}

// ExtractSubReaderContext writes a selected embedded track as SRT. It avoids
// copying the entire video when Android supplies a seekable content descriptor.
func ExtractSubReaderContext(ctx context.Context, ffmpeg string, track int, media io.ReadSeekCloser, out io.Writer) error {
	if track < 0 || out == nil {
		return ErrInvalidInput
	}
	err := withSubtitleReader(ctx, ffmpeg, media, func(ctx context.Context, input string, stdin io.Reader) error {
		binary, err := ResolveFFmpegPath(ffmpeg)
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, binary, "-nostdin", "-v", "error", "-i", input,
			"-map", "0:s:"+strconv.Itoa(track), "-f", "srt", "pipe:1")
		setSysProcAttr(cmd)
		var stderr strings.Builder
		cmd.WaitDelay, cmd.Stdin, cmd.Stdout, cmd.Stderr = time.Second, stdin, out, &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("subtitle extraction: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("ExtractSubReaderContext: %w", err)
	}
	return nil
}

func withSubtitleReader(ctx context.Context, ffmpeg string, media io.ReadSeekCloser, run func(context.Context, string, io.Reader) error) (err error) {
	if media == nil {
		return ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	stopClosing := context.AfterFunc(ctx, func() { _ = media.Close() })
	defer stopClosing()
	input, stdin := "pipe:0", io.Reader(media)
	switch {
	case progressiveReaderURL(media) != "":
		input, stdin = progressiveReaderURL(media), nil
	default:
		if file, ok := underlyingOSFile(media); ok {
			input, stdin = ffmpegInputForFileContext(ctx, ffmpeg, file), file
		}
		pos, seekErr := media.Seek(0, io.SeekCurrent)
		if seekErr == nil {
			if _, err := media.Seek(0, io.SeekStart); err != nil {
				return err
			}
			defer func() {
				if _, restoreErr := media.Seek(pos, io.SeekStart); err == nil {
					err = restoreErr
				}
			}()
		}
	}
	err = run(ctx, input, stdin)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
