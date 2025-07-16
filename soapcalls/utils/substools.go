//go:build !windows
// +build !windows

package utils

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/go-viper/mapstructure/v2"
)

type ffprobeInfoforSubs struct {
	Streams []streams `json:"streams"`
}

type streams struct {
	Tags      any    `json:"tags,omitempty"`
	CodecType string `json:"codec_type"`
	Index     int    `json:"index"`
}

type tags struct {
	Title    string `mapstructure:"title"`
	Language string `mapstructure:"language"`
}

// ErrNoSubs - No subs detected
var ErrNoSubs = errors.New("no subs")

// GetSubs - List all subs in our video file.
func GetSubs(ffmpeg string, f string) ([]string, error) {
	_, err := os.Stat(f)
	if err != nil {
		return nil, err
	}

	// We assume the ffprobe path based on the ffmpeg one.
	// So we need to ensure that the ffmpeg one exists.
	if err := CheckFFmpeg(ffmpeg); err != nil {
		return nil, err
	}

	cmd := exec.Command(
		filepath.Join(filepath.Dir(ffmpeg), "ffprobe"),
		"-loglevel", "error",
		"-show_streams",
		"-of", "json",
		f,
	)

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var info ffprobeInfoforSubs

	if err := json.Unmarshal(output, &info); err != nil {
		return nil, err
	}

	out := make([]string, 0)

	var subcounter int
	for _, s := range info.Streams {
		if s.CodecType == "subtitle" {
			subcounter++
			tag := &tags{}
			if err := mapstructure.Decode(s.Tags, tag); err != nil {
				return nil, err
			}

			subName := tag.Title
			if tag.Title == "" {
				subName = tag.Language
			}

			if subName == "" {
				subName = strconv.Itoa(subcounter)
			}

			out = append(out, subName)
		}
	}

	if len(out) == 0 {
		return nil, ErrNoSubs
	}

	return out, nil
}

// ExtractSub - Save the extracted sub into a temp file.
// Return the path of that file.
func ExtractSub(ffmpeg string, n int, f string) (string, error) {
	_, err := os.Stat(f)
	if err != nil {
		return "", err
	}

	tempSub, err := os.CreateTemp(os.TempDir(), "go2tv-sub-*.srt")
	if err != nil {
		return "", err
	}

	cmd := exec.Command(
		ffmpeg,
		"-y",
		"-i", f,
		"-map", "0:s:"+strconv.Itoa(n),
		tempSub.Name(),
	)

	_, err = cmd.Output()
	if err != nil {
		return "", err
	}

	return tempSub.Name(), nil
}
