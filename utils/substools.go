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

// ErrBitmapSubtitles identifies captions requiring video burn-in, not text extraction.
var ErrBitmapSubtitles = errors.New("image-based subtitles cannot be converted to text")

type subtitleTrackInfo struct {
	Streams []struct {
		CodecName string `json:"codec_name"`
	} `json:"streams"`
	Format struct {
		Name      string `json:"format_name"`
		StartTime string `json:"start_time"`
	} `json:"format"`
}

func bitmapSubtitleCodec(codec string) bool {
	switch codec {
	case "hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub":
		return true
	}
	return false
}

func probeSubtitleTrackContext(ctx context.Context, ffmpeg, path string, index int) (subtitleTrackInfo, error) {
	var info subtitleTrackInfo
	if err := ctx.Err(); err != nil {
		return info, err
	}
	if index < 0 {
		return info, fmt.Errorf("invalid subtitle track")
	}
	ffprobe, err := ResolveFFprobePath(ffmpeg)
	if err != nil {
		return info, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "s:"+strconv.Itoa(index),
		"-show_entries", "stream=codec_name:format=format_name,start_time", "-of", "json", path)
	setSysProcAttr(cmd)
	cmd.WaitDelay = time.Second
	data, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return info, ctx.Err()
		}
		return info, fmt.Errorf("probe subtitle track: %w", err)
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return info, fmt.Errorf("decode subtitle track: %w", err)
	}
	return info, nil
}

// GetSubs - List all subs in our video file.
func GetSubs(ffmpeg string, f string) ([]string, error) {
	return GetSubsContext(context.Background(), ffmpeg, f)
}

// GetSubsContext cancels subtitle discovery when playback preparation stops.
func GetSubsContext(ctx context.Context, ffmpeg string, f string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := mediaInput(f)
	if err != nil {
		return nil, err
	}

	// We assume the ffprobe path based on the ffmpeg one.
	// So we need to ensure that the ffmpeg one exists.
	if err := CheckFFmpegContext(ctx, ffmpeg); err != nil {
		return nil, err
	}

	ffprobePath, err := ResolveFFprobePath(ffmpeg)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx,
		ffprobePath,
		"-loglevel", "error",
		"-show_streams",
		"-of", "json",
		f,
	)
	setSysProcAttr(cmd)
	cmd.WaitDelay = time.Second

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
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
	return ExtractSubContext(context.Background(), ffmpeg, n, f)
}

// ExtractSubContext cancels both native parsing and FFmpeg fallback, including
// preparation before the renderer or media server has been created.
func ExtractSubContext(ctx context.Context, ffmpeg string, n int, f string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := mediaInput(f)
	if err != nil {
		return "", err
	}
	probe, probeErr := probeSubtitleTrackContext(ctx, ffmpeg, f, n)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if probeErr == nil && len(probe.Streams) == 1 && bitmapSubtitleCodec(probe.Streams[0].CodecName) {
		return "", fmt.Errorf("subtitle track %d: %w", n+1, ErrBitmapSubtitles)
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
	nativeErr := probeErr
	if probeErr == nil {
		nativeErr = extractNativeSub(ctx, n, f, tempSub, probe)
	}
	if err := tempSub.Close(); err != nil {
		return "", fmt.Errorf("close subtitle file: %w", err)
	}
	if nativeErr == nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		success = true
		return subPath, nil
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	resolvedFFmpeg, err := ResolveFFmpegPath(ffmpeg)
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx,
		resolvedFFmpeg,
		"-nostdin", "-loglevel", "error",
		"-y",
		"-i", f,
		"-map", "0:s:"+strconv.Itoa(n),
		subPath,
	)
	setSysProcAttr(cmd)
	cmd.WaitDelay = time.Second

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("extract subtitle track %d: %w: %s", n+1, err, strings.TrimSpace(string(output)))
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	success = true
	return subPath, nil
}

func extractNativeSub(ctx context.Context, index int, path string, out io.Writer, probe subtitleTrackInfo) error {
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
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	// FFmpeg normalizes subtitles to the media start time. Also confirm the
	// selected codec using the same stream selector as the fallback command.
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
