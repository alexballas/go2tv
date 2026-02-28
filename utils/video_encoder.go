package utils

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const transcodeEncoderProbeTimeout = 5 * time.Second

type videoEncoderProfile string

const (
	videoEncoderProfileDLNA           videoEncoderProfile = "dlna"
	videoEncoderProfileChromecastFile videoEncoderProfile = "chromecast_file"
	videoEncoderProfileChromecastRaw  videoEncoderProfile = "chromecast_raw"
)

type videoEncoderPlan struct {
	codec      string
	hardware   bool
	globalArgs []string
	filterTail string
	codecArgs  []string
}

var transcodeVideoEncoderCache sync.Map

func selectTranscodeVideoEncoder(ffmpegPath string, profile videoEncoderProfile) videoEncoderPlan {
	key := transcodeEncoderCacheKey(ffmpegPath, profile)
	if cached, ok := transcodeVideoEncoderCache.Load(key); ok {
		return cached.(videoEncoderPlan)
	}

	plan := selectTranscodeVideoEncoderUncached(ffmpegPath, profile)
	transcodeVideoEncoderCache.Store(key, plan)

	return plan
}

func transcodeEncoderCacheKey(ffmpegPath string, profile videoEncoderProfile) string {
	return ffmpegPath + "|" + string(profile)
}

func selectTranscodeVideoEncoderUncached(ffmpegPath string, profile videoEncoderProfile) videoEncoderPlan {
	software := transcodeSoftwareEncoderPlan(profile)
	candidates := transcodeHardwareEncoderCandidates(profile)
	if len(candidates) == 0 {
		return software
	}

	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return software
	}

	available, err := ffmpegVideoEncoderSet(ffmpegPath)
	if err != nil {
		available = nil
	}

	for _, candidate := range candidates {
		if len(available) > 0 {
			if _, ok := available[candidate.codec]; !ok {
				continue
			}
		}
		if err := probeTranscodeVideoEncoder(ffmpegPath, candidate); err == nil {
			return candidate
		}
	}

	return software
}

func transcodeSoftwareEncoderPlan(profile videoEncoderProfile) videoEncoderPlan {
	return videoEncoderPlan{
		codec:      "libx264",
		hardware:   false,
		filterTail: "format=yuv420p",
		codecArgs:  transcodeSoftwareCodecArgs(profile),
	}
}

func transcodeHardwareEncoderCandidates(profile videoEncoderProfile) []videoEncoderPlan {
	switch runtime.GOOS {
	case "darwin":
		return []videoEncoderPlan{
			transcodeHardwareEncoderPlan(profile, "h264_videotoolbox", nil),
		}
	case "windows":
		return []videoEncoderPlan{
			transcodeHardwareEncoderPlan(profile, "h264_nvenc", nil),
			transcodeHardwareEncoderPlan(profile, "h264_amf", nil),
			transcodeHardwareEncoderPlan(profile, "h264_qsv", nil),
		}
	default:
		candidates := []videoEncoderPlan{
			transcodeHardwareEncoderPlan(profile, "h264_nvenc", nil),
		}

		// Common on Raspberry Pi and other Linux SBCs with V4L2 M2M.
		candidates = append(candidates, transcodeHardwareEncoderPlan(profile, "h264_v4l2m2m", nil))

		// Legacy Raspberry Pi stacks may still expose OMX encoder.
		candidates = append(candidates, transcodeHardwareEncoderPlan(profile, "h264_omx", nil))

		devices, err := filepath.Glob("/dev/dri/renderD*")
		if err == nil {
			for _, dev := range devices {
				candidates = append(candidates, transcodeHardwareEncoderPlan(profile, "h264_vaapi", []string{"-vaapi_device", dev}))
			}
		}

		candidates = append(candidates, transcodeHardwareEncoderPlan(profile, "h264_qsv", nil))
		return candidates
	}
}

func transcodeHardwareEncoderPlan(profile videoEncoderProfile, codec string, globalArgs []string) videoEncoderPlan {
	return videoEncoderPlan{
		codec:      codec,
		hardware:   true,
		globalArgs: append([]string(nil), globalArgs...),
		filterTail: transcodeHardwareFilterTail(codec),
		codecArgs:  transcodeHardwareCodecArgs(profile, codec),
	}
}

func transcodeHardwareFilterTail(codec string) string {
	switch codec {
	case "h264_vaapi":
		return "format=nv12,hwupload"
	case "h264_qsv":
		return "format=nv12"
	default:
		return "format=yuv420p"
	}
}

func transcodeSoftwareCodecArgs(profile videoEncoderProfile) []string {
	switch profile {
	case videoEncoderProfileDLNA:
		return []string{
			"-c:v", "libx264",
			"-preset", "ultrafast",
			"-tune", "zerolatency",
			"-g", "30",
			"-keyint_min", "15",
			"-sc_threshold", "0",
		}
	case videoEncoderProfileChromecastRaw:
		return []string{
			"-c:v", "libx264",
			"-profile:v", "high",
			"-level", "4.1",
			"-preset", "ultrafast",
			"-tune", "zerolatency",
			"-crf", "23",
			"-g", "30",
			"-keyint_min", "30",
			"-sc_threshold", "0",
			"-bf", "0",
			"-maxrate", "5M",
			"-bufsize", "1M",
		}
	default:
		return []string{
			"-c:v", "libx264",
			"-profile:v", "high",
			"-level", "4.1",
			"-preset", "ultrafast",
			"-tune", "zerolatency",
			"-crf", "23",
			"-maxrate", "10M",
			"-bufsize", "20M",
		}
	}
}

func transcodeHardwareCodecArgs(profile videoEncoderProfile, codec string) []string {
	switch profile {
	case videoEncoderProfileDLNA:
		return []string{
			"-c:v", codec,
			"-g", "30",
		}
	case videoEncoderProfileChromecastRaw:
		return []string{
			"-c:v", codec,
			"-profile:v", "high",
			"-g", "30",
			"-maxrate", "5M",
			"-bufsize", "1M",
		}
	default:
		return []string{
			"-c:v", codec,
			"-profile:v", "high",
			"-g", "30",
			"-b:v", "5M",
			"-maxrate", "10M",
			"-bufsize", "20M",
		}
	}
}

func ffmpegVideoEncoderSet(ffmpegPath string) (map[string]struct{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), transcodeEncoderProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-encoders")
	setSysProcAttr(cmd)

	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("ffmpeg -encoders timeout after %s", transcodeEncoderProbeTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("ffmpeg -encoders failed: %w", err)
	}

	encoders := make(map[string]struct{})
	lines := strings.SplitSeq(string(out), "\n")
	for line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		if strings.Contains(fields[0], "V") {
			encoders[fields[1]] = struct{}{}
		}
	}

	return encoders, nil
}

func probeTranscodeVideoEncoder(ffmpegPath string, plan videoEncoderPlan) error {
	ctx, cancel := context.WithTimeout(context.Background(), transcodeEncoderProbeTimeout)
	defer cancel()

	filter := joinVideoFilters("scale=640:360", plan.filterTail)

	args := []string{
		"-v", "error",
		"-nostdin",
	}
	args = append(args, plan.globalArgs...)
	args = append(args,
		"-f", "lavfi",
		"-i", "color=c=black:s=1280x720:r=30:d=0.5",
		"-an",
		"-frames:v", "8",
		"-r", "30",
	)
	if filter != "" {
		args = append(args, "-vf", filter)
	}
	args = append(args, plan.codecArgs...)
	args = append(args, "-f", "null", "-")

	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	setSysProcAttr(cmd)

	var stderr bytes.Buffer
	cmd.Stdout = &stderr
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("probe timeout after %s", transcodeEncoderProbeTimeout)
	}
	if err != nil {
		return fmt.Errorf("probe failed: %w: %s", err, tailFFmpegStderr(strings.TrimSpace(stderr.String()), 240))
	}

	return nil
}

func joinVideoFilters(parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		nonEmpty = append(nonEmpty, trimmed)
	}
	return strings.Join(nonEmpty, ",")
}

func tailFFmpegStderr(input string, max int) string {
	if input == "" {
		return "no ffmpeg stderr output"
	}
	if max <= 0 || len(input) <= max {
		return input
	}
	return input[len(input)-max:]
}
