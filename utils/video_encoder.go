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

// Transcoding policy reference (target/max/buffer values are Mbps):
//
// Platform/codec       Policy
// NVIDIA NVENC/CUDA    p4, VBR 10/20/40; all profiles
// AMD AMF              balanced, vbr_peak; file 10/20/40
// Intel VAAPI          no preset/forced VBR; file 10/20/40; DLNA default
// Intel QSV             same as VAAPI; Windows
// VideoToolbox          fixed 5; no VBV
// MediaCodec            5/10/20
// V4L2/SBC              5/10/20
// Chromecast raw        5 max/1 buffer; NVENC 10/20/40
// Chromecast file       10/20/40 for NVENC/AMF/VAAPI/QSV; 5/10/20 otherwise
// DLNA                  NVENC 10/20/40; MediaCodec/V4L2 5/10/20; others default
// Software              CRF 23; raw 5/1 max/buffer; file 10/20 max/buffer; DLNA uncapped
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

	plan := selectTranscodeEncoderNoCache(ffmpegPath, profile)
	transcodeVideoEncoderCache.Store(key, plan)

	return plan
}

func transcodeEncoderCacheKey(ffmpegPath string, profile videoEncoderProfile) string {
	return ffmpegPath + "|" + string(profile)
}

func selectTranscodeEncoderNoCache(ffmpegPath string, profile videoEncoderProfile) videoEncoderPlan {
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
	case "android":
		return []videoEncoderPlan{
			transcodeHardwareEncoderPlan(profile, "h264_mediacodec", nil),
			transcodeHardwareEncoderPlan(profile, "h264_v4l2m2m", nil),
		}
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

// Keep CUDA frames on the GPU for decoding, scaling, and encoding.
const cudaTranscodeScaleFilter = "scale_cuda=w='min(1920,iw)':h='min(1080,ih)':format=yuv420p:force_original_aspect_ratio=decrease:force_divisible_by=2"

var cudaTranscodeCache sync.Map

func cudaTranscodeAvailable(ffmpegPath string) bool {
	if cached, ok := cudaTranscodeCache.Load(ffmpegPath); ok {
		return cached.(bool)
	}
	ok := probeCudaTranscode(ffmpegPath)
	cudaTranscodeCache.Store(ffmpegPath, ok)
	return ok
}

func probeCudaTranscode(ffmpegPath string) bool {
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return false
	}
	if !ffmpegFilterAvailable(ffmpegPath, "scale_cuda") {
		return false
	}
	if !ffmpegFilterAvailable(ffmpegPath, "hwupload_cuda") {
		return false
	}
	if !ffmpegHwaccelAvailable(ffmpegPath, "cuda") {
		return false
	}
	available, err := ffmpegVideoEncoderSet(ffmpegPath)
	if err != nil {
		return false
	}
	if len(available) > 0 {
		if _, ok := available["h264_nvenc"]; !ok {
			return false
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), transcodeEncoderProbeTimeout)
	defer cancel()

	args := []string{
		"-v", "error",
		"-nostdin",
		"-f", "lavfi",
		"-i", "color=c=black:s=1280x720:r=30:d=0.5",
		"-an",
		"-frames:v", "8",
		"-r", "30",
		"-vf", "hwupload_cuda,scale_cuda=640:360:format=yuv420p",
		"-c:v", "h264_nvenc",
		"-f", "null", "-",
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	setSysProcAttr(cmd)

	if err := cmd.Run(); err != nil {
		return false
	}
	return ctx.Err() == nil
}

func ffmpegHwaccelAvailable(ffmpegPath, name string) bool {
	key := ffmpegPath + "|hwaccel|" + name
	if cached, ok := ffmpegFilterCache.Load(key); ok {
		return cached.(bool)
	}
	set, err := ffmpegHwaccelSet(ffmpegPath)
	if err != nil {
		return false
	}
	_, ok := set[name]
	ffmpegFilterCache.Store(key, ok)
	return ok
}

func ffmpegHwaccelSet(ffmpegPath string) (map[string]struct{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), transcodeEncoderProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-hwaccels")
	setSysProcAttr(cmd)

	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("ffmpeg -hwaccels timeout after %s", transcodeEncoderProbeTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("ffmpeg -hwaccels failed: %w", err)
	}

	accels := make(map[string]struct{})
	for line := range strings.SplitSeq(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.Contains(trimmed, ":") || strings.Contains(trimmed, " ") {
			continue
		}
		accels[trimmed] = struct{}{}
	}
	return accels, nil
}

// Keep VAAPI frames on the GPU for decoding, scaling, and encoding.
const vaapiTranscodeScaleFilter = "scale_vaapi=w='min(1920,iw)':h='min(1080,ih)':format=nv12:force_original_aspect_ratio=decrease:force_divisible_by=2"

var vaapiTranscodeCache sync.Map

func vaapiDeviceFromPlan(plan videoEncoderPlan) string {
	for i := 0; i+1 < len(plan.globalArgs); i++ {
		if plan.globalArgs[i] == "-vaapi_device" {
			return plan.globalArgs[i+1]
		}
	}
	return ""
}

func vaapiTranscodeAvailable(ffmpegPath, device string) bool {
	if device == "" {
		return false
	}
	key := ffmpegPath + "|" + device
	if cached, ok := vaapiTranscodeCache.Load(key); ok {
		return cached.(bool)
	}
	ok := probeVaapiTranscode(ffmpegPath, device)
	vaapiTranscodeCache.Store(key, ok)
	return ok
}

func probeVaapiTranscode(ffmpegPath, device string) bool {
	if _, err := exec.LookPath(ffmpegPath); err != nil {
		return false
	}
	if !ffmpegFilterAvailable(ffmpegPath, "scale_vaapi") {
		return false
	}
	if !ffmpegHwaccelAvailable(ffmpegPath, "vaapi") {
		return false
	}
	available, err := ffmpegVideoEncoderSet(ffmpegPath)
	if err != nil {
		return false
	}
	if len(available) > 0 {
		if _, ok := available["h264_vaapi"]; !ok {
			return false
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), transcodeEncoderProbeTimeout)
	defer cancel()

	args := []string{
		"-v", "error",
		"-nostdin",
		"-vaapi_device", device,
		"-f", "lavfi",
		"-i", "color=c=black:s=1280x720:r=30:d=0.5",
		"-an",
		"-frames:v", "8",
		"-r", "30",
		"-vf", "format=nv12,hwupload,scale_vaapi=640:360",
		"-c:v", "h264_vaapi",
		"-f", "null", "-",
	}
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	setSysProcAttr(cmd)

	if err := cmd.Run(); err != nil {
		return false
	}
	return ctx.Err() == nil
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
	args := transcodeHardwareBaseCodecArgs(profile, codec)
	switch codec {
	case "h264_nvenc":
		return append(args, "-preset", "p4")
	case "h264_amf":
		return append(args, "-quality", "balanced", "-rc", "vbr_peak")
	case "h264_videotoolbox":
		// VideoToolbox rate limits can cause slowdowns or encoder hangs.
		// Use an explicit bitrate on every profile, without VBV constraints.
		args = []string{"-c:v", codec}
		if profile != videoEncoderProfileDLNA {
			args = append(args, "-profile:v", "high")
		}
		return append(args, "-g", "30", "-b:v", "5M", "-qmin", "-1", "-qmax", "-1")
	default:
		// Do not force VAAPI VBR: Intel i965 can produce pixelated output.
		return args
	}
}

func transcodeHardwareBaseCodecArgs(profile videoEncoderProfile, codec string) []string {
	if codec == "h264_nvenc" {
		args := []string{"-c:v", codec}
		if profile != videoEncoderProfileDLNA {
			args = append(args, "-profile:v", "high")
		}
		return append(args, "-g", "30", "-rc", "vbr", "-b:v", "10M", "-maxrate", "20M", "-bufsize", "40M")
	}
	if profile == videoEncoderProfileChromecastFile &&
		(codec == "h264_amf" || codec == "h264_vaapi" || codec == "h264_qsv") {
		return []string{
			"-c:v", codec,
			"-profile:v", "high",
			"-g", "30",
			"-b:v", "10M",
			"-maxrate", "20M",
			"-bufsize", "40M",
		}
	}

	switch profile {
	case videoEncoderProfileDLNA:
		args := []string{
			"-c:v", codec,
			"-g", "30",
		}
		// MediaCodec/V4L2 M2M default to very low bitrates, producing
		// blocky output; request a proper streaming bitrate explicitly.
		if codec == "h264_mediacodec" || codec == "h264_v4l2m2m" {
			args = append(args, "-b:v", "5M", "-maxrate", "10M", "-bufsize", "20M")
		}
		return args
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
	args = append(
		args,
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
