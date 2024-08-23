package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/mitchellh/mapstructure"
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

var ErrNoSubs = errors.New("no subs")

func GetSubs(f string) ([]string, error) {
	_, err := os.Stat(f)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	cmd := exec.Command(
		"ffprobe",
		"-loglevel", "error",
		"-show_streams",
		"-of", "json",
		f,
	)

	output, err := cmd.Output()
	if err != nil {
		fmt.Println(err)
		return nil, err
	}

	var info ffprobeInfoforSubs

	if err := json.Unmarshal(output, &info); err != nil {
		fmt.Println(err)
		return nil, err
	}

	out := make([]string, 0)

	var subcounter int
	for _, s := range info.Streams {
		if s.CodecType == "subtitle" {
			subcounter++
			tag := &tags{}
			if err := mapstructure.Decode(s.Tags, tag); err != nil {
				fmt.Println(err)
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
		fmt.Println(ErrNoSubs)
		return nil, ErrNoSubs
	}

	return out, nil
}

func ExtractSub(n int, f string) (string, error) {
	_, err := os.Stat(f)
	if err != nil {
		return "", err
	}

	tempSub, err := os.CreateTemp(os.TempDir(), "go2tv-sub-*.srt")
	if err != nil {
		return "", err
	}

	cmd := exec.Command(
		"ffmpeg",
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
