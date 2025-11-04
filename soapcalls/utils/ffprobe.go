//go:build !windows

package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type ffprobeInfo struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func DurationForMedia(ffmpeg string, f string) (string, error) {
	_, err := os.Stat(f)
	if err != nil {
		return "", err
	}

	if err := CheckFFmpeg(ffmpeg); err != nil {
		return "", err
	}

	cmd := exec.Command(
		filepath.Join(filepath.Dir(ffmpeg), "ffprobe"),
		"-loglevel", "error",
		"-show_format",
		"-of", "json",
		f,
	)

	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	var info ffprobeInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return "", err
	}

	ff, err := strconv.ParseFloat(info.Format.Duration, 64)
	if err != nil {
		return "", err
	}

	duration := time.Duration(ff * float64(time.Second))
	return formatDuration(duration), nil
}

func formatDuration(t time.Duration) string {
	t /= time.Millisecond
	ms := t % 1000
	t /= 1000
	s := t % 60
	t /= 60
	m := t % 60
	t /= 60
	h := t
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
