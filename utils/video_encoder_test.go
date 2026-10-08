package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestTranscodeHardwareCodecArgs(t *testing.T) {
	tt := []struct {
		codec, dlna, raw, file string
	}{
		{"h264_nvenc", "-g 30 -rc vbr -b:v 10M -maxrate 20M -bufsize 40M -preset p4", "-profile:v high -g 30 -rc vbr -b:v 10M -maxrate 20M -bufsize 40M -preset p4", "-profile:v high -g 30 -rc vbr -b:v 10M -maxrate 20M -bufsize 40M -preset p4"},
		{"h264_amf", "-g 30 -quality balanced -rc vbr_peak", "-profile:v high -g 30 -maxrate 5M -bufsize 1M -quality balanced -rc vbr_peak", "-profile:v high -g 30 -b:v 10M -maxrate 20M -bufsize 40M -quality balanced -rc vbr_peak"},
		{"h264_videotoolbox", "-g 30 -b:v 5M -qmin -1 -qmax -1", "-profile:v high -g 30 -b:v 5M -qmin -1 -qmax -1", "-profile:v high -g 30 -b:v 5M -qmin -1 -qmax -1"},
		{"h264_vaapi", "-g 30", "-profile:v high -g 30 -maxrate 5M -bufsize 1M", "-profile:v high -g 30 -b:v 10M -maxrate 20M -bufsize 40M"},
		{"h264_qsv", "-g 30", "-profile:v high -g 30 -maxrate 5M -bufsize 1M", "-profile:v high -g 30 -b:v 10M -maxrate 20M -bufsize 40M"},
		{"h264_mediacodec", "-g 30 -b:v 5M -maxrate 10M -bufsize 20M", "-profile:v high -g 30 -maxrate 5M -bufsize 1M", "-profile:v high -g 30 -b:v 5M -maxrate 10M -bufsize 20M"},
		{"h264_v4l2m2m", "-g 30 -b:v 5M -maxrate 10M -bufsize 20M", "-profile:v high -g 30 -maxrate 5M -bufsize 1M", "-profile:v high -g 30 -b:v 5M -maxrate 10M -bufsize 20M"},
		{"h264_omx", "-g 30", "-profile:v high -g 30 -maxrate 5M -bufsize 1M", "-profile:v high -g 30 -b:v 5M -maxrate 10M -bufsize 20M"},
	}
	for _, tc := range tt {
		for _, profile := range []struct {
			name videoEncoderProfile
			want string
		}{
			{videoEncoderProfileDLNA, tc.dlna},
			{videoEncoderProfileChromecastRaw, tc.raw},
			{videoEncoderProfileChromecastFile, tc.file},
		} {
			t.Run(tc.codec+"/"+string(profile.name), func(t *testing.T) {
				want := append([]string{"-c:v", tc.codec}, strings.Fields(profile.want)...)
				if got := transcodeHardwareCodecArgs(profile.name, tc.codec); !slices.Equal(got, want) {
					t.Fatalf("args = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestSelectTranscodeEncoderFallsBackToSoftware(t *testing.T) {
	plan := selectTranscodeVideoEncoder("/path/does/not/exist/ffmpeg", videoEncoderProfileChromecastFile)
	if plan.codec != "libx264" {
		t.Fatalf("expected libx264 fallback, got %q", plan.codec)
	}
	if plan.hardware {
		t.Fatalf("expected software fallback, got hardware plan %+v", plan)
	}
}

func TestSelectTranscodeEncoderUsesHardware(t *testing.T) {
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

func TestSelectTranscodeEncoderFallsBackOnProbeFailure(t *testing.T) {
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
