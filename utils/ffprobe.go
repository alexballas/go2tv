//go:build !windows

package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	seconds, err := DurationForMediaSeconds(ffmpeg, f)
	if err != nil {
		return "", err
	}

	duration := time.Duration(seconds * float64(time.Second))
	return formatDuration(duration), nil
}

// DurationForMediaSeconds returns the media duration in seconds.
// Used for Chromecast transcoded streams where we need the raw duration value.
func DurationForMediaSeconds(ffmpeg string, f string) (float64, error) {
	_, err := os.Stat(f)
	if err != nil {
		return 0, err
	}

	if err := CheckFFmpeg(ffmpeg); err != nil {
		return 0, err
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
		return 0, err
	}

	var info ffprobeInfo
	if err := json.Unmarshal(output, &info); err != nil {
		return 0, err
	}

	return strconv.ParseFloat(info.Format.Duration, 64)
}

func GetMediaCodecInfo(ffmpeg string, f string) (*MediaCodecInfo, error) {
	_, err := os.Stat(f)
	if err != nil {
		return nil, err
	}

	if err := CheckFFmpeg(ffmpeg); err != nil {
		return nil, err
	}

	cmd := exec.Command(
		filepath.Join(filepath.Dir(ffmpeg), "ffprobe"),
		"-loglevel", "error",
		"-show_format",
		"-show_streams",
		"-of", "json",
		f,
	)

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
