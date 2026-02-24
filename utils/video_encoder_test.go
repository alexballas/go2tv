package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSelectTranscodeVideoEncoderFallsBackToSoftware(t *testing.T) {
	plan := selectTranscodeVideoEncoder("/path/does/not/exist/ffmpeg", videoEncoderProfileChromecastFile)
	if plan.codec != "libx264" {
		t.Fatalf("expected libx264 fallback, got %q", plan.codec)
	}
	if plan.hardware {
		t.Fatalf("expected software fallback, got hardware plan %+v", plan)
	}
}

func TestSelectTranscodeVideoEncoderUsesWorkingHardware(t *testing.T) {
	candidates := transcodeHardwareEncoderCandidates(videoEncoderProfileChromecastFile)
	if len(candidates) == 0 {
		t.Skip("no hardware encoder candidates for this platform")
	}

	expectedCodec := candidates[0].codec
	ffmpegPath := writeFakeTranscodeFFmpeg(t)
	t.Setenv("FAKE_SUPPORTED_CODEC", expectedCodec)

	plan := selectTranscodeVideoEncoder(ffmpegPath, videoEncoderProfileChromecastFile)
	if plan.codec != expectedCodec {
		t.Fatalf("expected codec %q, got %q", expectedCodec, plan.codec)
	}
	if !plan.hardware {
		t.Fatalf("expected hardware plan for codec %q", expectedCodec)
	}
}

func TestSelectTranscodeVideoEncoderFallsBackWhenProbesFail(t *testing.T) {
	ffmpegPath := writeFakeTranscodeFFmpeg(t)
	t.Setenv("FAKE_SUPPORTED_CODEC", "")

	plan := selectTranscodeVideoEncoder(ffmpegPath, videoEncoderProfileChromecastFile)
	if plan.codec != "libx264" {
		t.Fatalf("expected libx264 fallback when probes fail, got %q", plan.codec)
	}
	if plan.hardware {
		t.Fatalf("expected software fallback when probes fail, got hardware plan %+v", plan)
	}
}

func writeFakeTranscodeFFmpeg(t *testing.T) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		return writeFakeTranscodeFFmpegWindows(t)
	}

	script := `#!/bin/sh
if [ "$1" = "-hide_banner" ] && [ "$2" = "-encoders" ]; then
  echo "Encoders:"
  echo " V..... h264_nvenc           fake"
  echo " V..... h264_amf             fake"
  echo " V..... h264_qsv             fake"
  echo " V..... h264_vaapi           fake"
  echo " V..... h264_videotoolbox    fake"
  exit 0
fi
supported="$FAKE_SUPPORTED_CODEC"
for arg in "$@"; do
  if [ -n "$supported" ] && [ "$arg" = "$supported" ]; then
    exit 0
  fi
done
echo "unsupported codec" >&2
exit 1
`

	path := filepath.Join(t.TempDir(), "fake-ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}

func writeFakeTranscodeFFmpegWindows(t *testing.T) string {
	t.Helper()

	script := `@echo off
if "%1"=="-hide_banner" if "%2"=="-encoders" (
  echo Encoders:
  echo  V..... h264_nvenc           fake
  echo  V..... h264_amf             fake
  echo  V..... h264_qsv             fake
  echo  V..... h264_vaapi           fake
  echo  V..... h264_videotoolbox    fake
  exit /b 0
)
set "supported=%FAKE_SUPPORTED_CODEC%"
:args
if "%1"=="" goto unsupported
if not "%supported%"=="" if "%1"=="%supported%" exit /b 0
shift
goto args
:unsupported
echo unsupported codec 1>&2
exit /b 1
`

	path := filepath.Join(t.TempDir(), "fake-ffmpeg.cmd")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}
