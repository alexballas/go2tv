package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func transcodeInputArgs(plan videoEncoderPlan, hw string) []string {
	args := append([]string(nil), plan.globalArgs...)
	switch hw {
	case "cuda", "vaapi":
		args = append(args, "-hwaccel", hw, "-hwaccel_output_format", hw)
	case "videotoolbox", "d3d11va", "drm":
		args = append(args, "-hwaccel", hw)
	}
	return args
}

func selectTranscodeVideoDecoder(ffmpegPath string, plan videoEncoderPlan, subFilter, in string, isRaw bool) string {
	if isRaw || subFilter != "" || in == "pipe:0" {
		return ""
	}
	switch plan.codec {
	case "h264_nvenc":
		if cudaTranscodeAvailable(ffmpegPath) {
			return "cuda"
		}
	case "h264_vaapi":
		if vaapiTranscodeAvailable(ffmpegPath, vaapiDeviceFromPlan(plan)) {
			return "vaapi"
		}
	}
	decoder := transcodeDownloadDecoder(runtime.GOOS, plan.codec)
	if decoder != "" && ffmpegHwaccelAvailable(ffmpegPath, decoder) {
		return decoder
	}
	// Pi 4 can pair HEVC decoding with V4L2 H.264 encoding; Pi 5 needs
	// libx264. Decoder availability must not depend on a hardware encoder.
	// FFmpeg selects the input codec: HEVC can use the request driver, while
	// unsupported codecs decode in software or trigger the startup retry.
	// Leave output format unset so Pi FFmpeg converts its SAND frames for
	// the existing CPU filters instead of passing DRM surfaces to them.
	if runtime.GOOS == "linux" && raspberryPiTranscodeAvailable(ffmpegPath, "/sys/class/video4linux") {
		return "drm"
	}
	return ""
}

func raspberryPiTranscodeAvailable(ffmpegPath, videoClassDir string) bool {
	names, err := filepath.Glob(filepath.Join(videoClassDir, "video*", "name"))
	if err != nil {
		return false
	}
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		switch strings.TrimSpace(string(data)) {
		case "rpi-hevc-dec", "rpivid":
			// Require the actual Pi HEVC driver, not merely a DRM render node
			// or a Pi model name. Other Linux GPUs must not select this path.
			// Older Raspberry Pi kernels call the same decoder "rpivid".
			return ffmpegHwaccelAvailable(ffmpegPath, "drm")
		}
	}
	return false
}

// These backends download decoded frames before the existing software filters.
// Do not set hwaccel_output_format: FFmpeg chooses the native download format,
// including P010 for 10-bit inputs. Forcing NV12 would reject those inputs.
// -hwaccels only checks the build; actual device/codec support is tried on the
// requested input, with startup failures handled by runTranscodeWithFallback.
func transcodeDownloadDecoder(goos, codec string) string {
	switch {
	case goos == "darwin" && codec == "h264_videotoolbox":
		return "videotoolbox"
	case goos == "windows" && (codec == "h264_amf" || codec == "h264_qsv"):
		return "d3d11va"
	default:
		return ""
	}
}
