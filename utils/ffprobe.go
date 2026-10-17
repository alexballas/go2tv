package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type ffprobeInfo struct {
	Format struct {
		Duration   string `json:"duration"`
		FormatName string `json:"format_name"`
		BitRate    string `json:"bit_rate"`
	} `json:"format"`
	Streams []struct {
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		Profile    string `json:"profile"`
		Channels   int    `json:"channels"`
		SampleRate string `json:"sample_rate"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
	} `json:"streams"`
}

type MediaCodecInfo struct {
	VideoCodec    string
	VideoProfile  string
	AudioCodec    string
	AudioChannels int
	Container     string
}

func DurationForMedia(ffmpeg string, f string) (string, error) {
	return DurationForMediaContext(context.Background(), ffmpeg, f)
}

func DurationForMediaContext(ctx context.Context, ffmpeg string, f string) (string, error) {
	seconds, err := DurationForMediaSecondsContext(ctx, ffmpeg, f)
	if err != nil {
		return "", err
	}

	duration := time.Duration(seconds * float64(time.Second))
	return formatDuration(duration), nil
}

// DurationForMediaSeconds returns the media duration in seconds.
// Transcoded streams use it to expose the source timeline.
func DurationForMediaSeconds(ffmpeg string, f string) (float64, error) {
	return DurationForMediaSecondsContext(context.Background(), ffmpeg, f)
}

// DurationForMediaSecondsContext allows pending media probes to be cancelled.
func DurationForMediaSecondsContext(ctx context.Context, ffmpeg string, f string) (float64, error) {
	if ctx == nil {
		return 0, errors.New("ffprobe context required")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	f, err := mediaInput(f)
	if err != nil {
		return 0, err
	}

	if err := CheckFFmpegContext(ctx, ffmpeg); err != nil {
		return 0, err
	}

	ffprobePath, err := ResolveFFprobePath(ffmpeg)
	if err != nil {
		return 0, err
	}

	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx,
		ffprobePath,
		"-loglevel", "error",
		"-show_format",
		"-of", "json",
		f,
	)
	setSysProcAttr(cmd)
	cmd.WaitDelay = time.Second

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, err
	}

	var info ffprobeInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return 0, err
	}

	return strconv.ParseFloat(info.Format.Duration, 64)
}

// DurationForMediaReaderSeconds returns the media duration in seconds without
// requiring a filesystem path. Seekable files use ffmpeg's fd protocol;
// other readers fall back to stdin's pipe protocol. The input offset is
// restored when the reader supports seeking.
func DurationForMediaReaderSeconds(ctx context.Context, ffmpeg string, media io.ReadSeekCloser) (seconds float64, err error) {
	if ctx == nil {
		return 0, errors.New("ffprobe context required")
	}
	if media == nil {
		return 0, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	stopClosing := context.AfterFunc(ctx, func() { _ = media.Close() })
	defer stopClosing()
	ffprobePath, err := ResolveFFprobePath(ffmpeg)
	if err != nil {
		return 0, err
	}

	input := io.Reader(media)
	inputURL := "pipe:0"
	if url := progressiveReaderURL(media); url != "" {
		input, inputURL = nil, url
	} else if file, ok := underlyingOSFile(media); ok {
		input = file
		inputURL = ffmpegInputForFileContext(ctx, ffmpeg, file)
	}

	if seeker, ok := input.(io.Seeker); ok {
		offset, seekErr := seeker.Seek(0, io.SeekCurrent)
		if seekErr == nil {
			if _, seekErr = seeker.Seek(0, io.SeekStart); seekErr != nil {
				return 0, fmt.Errorf("rewind ffprobe input: %w", seekErr)
			}
			defer func() {
				if _, restoreErr := seeker.Seek(offset, io.SeekStart); err == nil && restoreErr != nil {
					err = fmt.Errorf("restore ffprobe input: %w", restoreErr)
				}
			}()
		}
	}

	cmd := exec.CommandContext(
		ctx,
		ffprobePath,
		"-loglevel", "error",
		"-show_format",
		"-of", "json",
		inputURL,
	)
	setSysProcAttr(cmd)
	cmd.WaitDelay = time.Second
	cmd.Stdin = input
	output, err := cmd.Output()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	if err != nil {
		return 0, fmt.Errorf("ffprobe duration: %w", err)
	}

	var info ffprobeInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return 0, fmt.Errorf("decode ffprobe duration: %w", err)
	}
	seconds, err = strconv.ParseFloat(info.Format.Duration, 64)
	if err != nil {
		return 0, fmt.Errorf("parse ffprobe duration: %w", err)
	}
	return seconds, nil
}

func GetMediaCodecInfo(ffmpeg string, f string) (*MediaCodecInfo, error) {
	return GetMediaCodecInfoContext(context.Background(), ffmpeg, f)
}

// GetMediaCodecInfoContext lets selection changes interrupt progressive probes.
func GetMediaCodecInfoContext(ctx context.Context, ffmpeg string, f string) (*MediaCodecInfo, error) {
	if ctx == nil {
		return nil, errors.New("ffprobe context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := mediaInput(f)
	if err != nil {
		return nil, err
	}

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
		"-show_format",
		"-show_streams",
		"-of", "json",
		f,
	)
	setSysProcAttr(cmd)

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var info ffprobeInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return nil, err
	}

	result := &MediaCodecInfo{
		Container: info.Format.FormatName,
	}

	for _, stream := range info.Streams {
		switch stream.CodecType {
		case "video":
			result.VideoCodec = stream.CodecName
			result.VideoProfile = stream.Profile
		case "audio":
			result.AudioCodec = stream.CodecName
			result.AudioChannels = stream.Channels
		}
	}

	return result, nil
}

type ffprobeRotation struct {
	Streams []struct {
		SideDataList []struct {
			Rotation int `json:"rotation"`
		} `json:"side_data_list"`
		Tags struct {
			Rotate string `json:"rotate"`
		} `json:"tags"`
	} `json:"streams"`
}

// videoRotationContext returns the first video stream's display rotation in
// degrees. in is the FFmpeg input URL; "fd:" probes the *os.File input.
func videoRotationContext(ctx context.Context, ffmpegPath string, input any, in string) (int, error) {
	ffprobePath, err := ResolveFFprobePath(ffmpegPath)
	if err != nil {
		return 0, fmt.Errorf("video rotation: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx,
		ffprobePath,
		"-loglevel", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream_side_data=rotation:stream_tags=rotate",
		"-of", "json",
		in,
	)
	setSysProcAttr(cmd)
	cmd.WaitDelay = time.Second

	switch in {
	case "fd:":
		// ffprobe shares the offset; runFFmpegTranscode rewinds again.
		f, ok := input.(*os.File)
		if !ok {
			return 0, ErrInvalidInput
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return 0, fmt.Errorf("video rotation: rewind input: %w", err)
		}
		cmd.Stdin = f
	case "pipe:0":
		// Probing would consume the stream FFmpeg still needs.
		return 0, ErrInvalidInput
	}

	output, err := cmd.Output()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	if err != nil {
		return 0, fmt.Errorf("video rotation: %w", err)
	}

	var info ffprobeRotation
	if err := json.Unmarshal(output, &info); err != nil {
		return 0, fmt.Errorf("video rotation: decode: %w", err)
	}
	if len(info.Streams) == 0 {
		return 0, nil
	}
	stream := info.Streams[0]
	for _, side := range stream.SideDataList {
		if side.Rotation != 0 {
			return side.Rotation, nil
		}
	}
	// FFmpeg before 5.0 also reported rotation as a stream tag.
	if stream.Tags.Rotate == "" {
		return 0, nil
	}
	rotation, err := strconv.Atoi(stream.Tags.Rotate)
	if err != nil {
		return 0, fmt.Errorf("video rotation: parse tag: %w", err)
	}
	return rotation, nil
}

func IsChromecastCompatible(info *MediaCodecInfo) bool {
	if info == nil {
		return false
	}

	supportedVideoCodecs := map[string]bool{
		"h264": true,
		"hevc": true,
		"vp8":  true,
		"vp9":  true,
		"av1":  true,
	}

	supportedAudioCodecs := map[string]bool{
		"aac":    true,
		"mp3":    true,
		"vorbis": true,
		"opus":   true,
		"flac":   true,
	}

	supportedContainers := map[string]bool{
		"mp4":      true,
		"mov":      true,
		"m4a":      true,
		"webm":     true,
		"matroska": true,
		"mkv":      true,
	}

	videoOK := info.VideoCodec == "" || supportedVideoCodecs[info.VideoCodec]
	audioOK := info.AudioCodec == "" || supportedAudioCodecs[info.AudioCodec]

	// ffprobe returns comma-separated format names (e.g., "matroska,webm" or "mov,mp4,m4a,3gp,3g2,mj2")
	// Check if any of the formats is supported
	containerOK := false
	for format := range strings.SplitSeq(info.Container, ",") {
		if supportedContainers[format] {
			containerOK = true
			break
		}
	}

	return videoOK && audioOK && containerOK
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
