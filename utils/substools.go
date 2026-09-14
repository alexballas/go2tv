package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

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

	ffprobePath, err := ResolveFFprobePath(ffmpeg)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(
		ffprobePath,
		"-loglevel", "error",
		"-show_streams",
		"-of", "json",
		f,
	)
	setSysProcAttr(cmd)

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var info ffprobeInfoforSubs

	if err := json.Unmarshal(output, &info); err != nil {
		return nil, err
	}

	out, err := subtitleNames(info.Streams)
	if err != nil {
		return nil, err
	}

	if len(out) == 0 {
		return nil, ErrNoSubs
	}

	return out, nil
}

func subtitleNames(streams []streams) ([]string, error) {
	out := make([]string, 0)

	var subcounter int
	for _, s := range streams {
		if s.CodecType == "subtitle" {
			subcounter++
			tag := &tags{}
			if err := mapstructure.Decode(s.Tags, tag); err != nil {
				return nil, err
			}

			subName := tag.Title

			switch {
			case tag.Title == "" && tag.Language != "":
				subName = tag.Language
			case tag.Language != "":
				subName += " (" + tag.Language + ")"
			}

			if subName == "" {
				subName = strconv.Itoa(subcounter)
			}

			out = append(out, subName)
		}
	}

	if len(out) > 1 {
		reNumber := true
		for _, s := range out {
			if s != out[0] {
				reNumber = false
				break
			}
		}

		if reNumber {
			for i := range out {
				out[i] = strconv.Itoa(i + 1)
			}
		}
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

	resolvedFFmpeg, err := ResolveFFmpegPath(ffmpeg)
	if err != nil {
		return "", err
	}

	tempSub, err := os.CreateTemp("", "go2tv-sub-*.srt")
	if err != nil {
		return "", err
	}
	subPath := tempSub.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(subPath)
		}
	}()
	if err := tempSub.Close(); err != nil {
		return "", fmt.Errorf("close subtitle file: %w", err)
	}

	cmd := exec.Command(
		resolvedFFmpeg,
		"-nostdin", "-loglevel", "error",
		"-y",
		"-i", f,
		"-map", "0:s:"+strconv.Itoa(n),
		subPath,
	)
	setSysProcAttr(cmd)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("extract subtitle track %d: %w: %s", n+1, err, strings.TrimSpace(string(output)))
	}

	success = true
	return subPath, nil
}
