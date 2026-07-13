package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

const maxVideoThumbnailBytes = 20 << 20
const videoThumbnailTimeout = 15 * time.Second

var videoDurationPattern = regexp.MustCompile(`Duration: (\d{2}):(\d{2}):(\d{2})\.(\d{2})`)

// ExtractVideoThumbnail extracts the middle frame from a path or confined file.
// Open files use ffmpeg's seekable fd input when available.
func ExtractVideoThumbnail(ctx context.Context, ffmpegPath string, input any) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, videoThumbnailTimeout)
	defer cancel()

	resolved, err := ResolveFFmpegPath(ffmpegPath)
	if err != nil {
		return nil, err
	}

	in, file, err := videoThumbnailInput(resolved, input)
	if err != nil {
		return nil, err
	}
	duration := videoInputDuration(ctx, resolved, in, file)
	seek := duration / 2
	if seek <= 0 {
		seek = time.Second
	}

	args := []string{"-hide_banner", "-loglevel", "error"}
	if in != "pipe:0" {
		args = append(args, "-ss", formatThumbnailSeek(seek), "-i", in)
	} else {
		args = append(args, "-i", in, "-ss", formatThumbnailSeek(seek))
	}
	args = append(args, "-frames:v", "1", "-f", "image2", "-vcodec", "mjpeg", "pipe:1")

	output := &boundedThumbnailBuffer{limit: maxVideoThumbnailBytes}
	stderr := &boundedThumbnailBuffer{limit: 64 << 10}
	cmd := exec.CommandContext(ctx, resolved, args...)
	setSysProcAttr(cmd)
	if file != nil {
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("rewind video thumbnail input: %w", err)
		}
		cmd.Stdin = file
	}
	cmd.Stdout = output
	cmd.Stderr = stderr
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if output.overflow {
			return nil, errors.New("video thumbnail exceeds 20 MiB")
		}
		return nil, fmt.Errorf("extract video thumbnail: %w: %s", err, tailFFmpegStderr(stderr.String(), 240))
	}
	if len(output.Bytes()) == 0 {
		return nil, errors.New("video thumbnail is empty")
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func videoThumbnailInput(ffmpegPath string, input any) (string, *os.File, error) {
	switch value := input.(type) {
	case string:
		if value == "" {
			return "", nil, ErrInvalidInput
		}
		return value, nil, nil
	case *os.File:
		if value == nil {
			return "", nil, ErrInvalidInput
		}
		return ffmpegInputForFile(ffmpegPath, value), value, nil
	default:
		return "", nil, ErrInvalidInput
	}
}

func videoInputDuration(ctx context.Context, ffmpegPath, input string, file *os.File) time.Duration {
	stderr := &boundedThumbnailBuffer{limit: 64 << 10}
	cmd := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-i", input)
	setSysProcAttr(cmd)
	if file != nil {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return 0
		}
		cmd.Stdin = file
	}
	cmd.Stderr = stderr
	_ = cmd.Run()
	return parseVideoDuration(stderr.String())
}

func parseVideoDuration(output string) time.Duration {
	match := videoDurationPattern.FindStringSubmatch(output)
	if len(match) != 5 {
		return 0
	}
	values := make([]int, 4)
	for i := range values {
		value, err := strconv.Atoi(match[i+1])
		if err != nil {
			return 0
		}
		values[i] = value
	}
	return time.Duration(values[0])*time.Hour +
		time.Duration(values[1])*time.Minute +
		time.Duration(values[2])*time.Second +
		time.Duration(values[3]*10)*time.Millisecond
}

func formatThumbnailSeek(seek time.Duration) string {
	return strconv.FormatFloat(seek.Seconds(), 'f', 3, 64)
}

type boundedThumbnailBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedThumbnailBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.overflow = true
		return 0, errors.New("buffer limit reached")
	}
	if len(data) > remaining {
		_, _ = b.Buffer.Write(data[:remaining])
		b.overflow = true
		return remaining, errors.New("buffer limit reached")
	}
	return b.Buffer.Write(data)
}
