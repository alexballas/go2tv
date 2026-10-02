package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"

	"go2tv.app/go2tv/v2/internal/mkvsubs"
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
	f, err := mediaInput(f)
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

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx,
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
	f, err := mediaInput(f)
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
	nativeErr := extractNativeSub(ffmpeg, n, f, tempSub)
	if err := tempSub.Close(); err != nil {
		return "", fmt.Errorf("close subtitle file: %w", err)
	}
	if nativeErr == nil {
		success = true
		return subPath, nil
	}

	resolvedFFmpeg, err := ResolveFFmpegPath(ffmpeg)
	if err != nil {
		return "", err
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

func extractNativeSub(ffmpeg string, index int, path string, out io.Writer) error {
	// Progressive sources keep their existing extraction path. Local files
	// with other extensions also retain FFmpeg's broader container support.
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".mkv" && ext != ".webm" {
		return mkvsubs.ErrNoSubtitles
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return mkvsubs.ErrNoSubtitles
	}
	ffprobe, err := ResolveFFprobePath(ffmpeg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	// FFmpeg normalizes subtitles to the media start time. Also confirm the
	// selected codec using the same stream selector as the fallback command.
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "s:"+strconv.Itoa(index),
		"-show_entries", "stream=codec_name:format=format_name,start_time", "-of", "json", path)
	setSysProcAttr(cmd)
	data, err := cmd.Output()
	if err != nil {
		return err
	}
	var probe struct {
		Streams []struct {
			CodecName string `json:"codec_name"`
		} `json:"streams"`
		Format struct {
			Name      string `json:"format_name"`
			StartTime string `json:"start_time"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	formats := strings.Split(probe.Format.Name, ",")
	if len(probe.Streams) != 1 || probe.Streams[0].CodecName != "subrip" ||
		(!slices.Contains(formats, "matroska") && !slices.Contains(formats, "webm")) {
		return mkvsubs.ErrNoSubtitles
	}
	startTime := 0.0
	if probe.Format.StartTime != "" {
		startTime, err = strconv.ParseFloat(probe.Format.StartTime, 64)
		if err != nil {
			return err
		}
	}
	return mkvsubs.ExtractFile(ctx, path, index, startTime, out)
}
