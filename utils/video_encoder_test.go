package utils

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestMediaCodecFormatSelection(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tt := []struct {
		name, format, encoders, want, codec string
		wantErr                             bool
	}{
		{"Qualcomm NV12", "nv12", "h264_mediacodec", "format=nv12", "h264_mediacodec", false},
		{"planar only", "yuv420p", "h264_mediacodec", "format=yuv420p", "h264_mediacodec", false},
		{"software fallback", "unsupported", "h264_mediacodec libx264", "format=yuv420p", "libx264", false},
		{"no working encoder", "unsupported", "h264_mediacodec", "", "", true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GO2TV_TEST_FFMPEG_HELPER", "1")
			t.Setenv("GO2TV_TEST_FFMPEG_CALLS", filepath.Join(t.TempDir(), "calls.json"))
			t.Setenv("GO2TV_TEST_FFMPEG_MODE", "format")
			t.Setenv("GO2TV_TEST_FFMPEG_FORMAT", tc.format)
			t.Setenv("GO2TV_TEST_FFMPEG_ENCODERS", tc.encoders)
			profile := videoEncoderProfileChromecastFile
			plan, err := selectTranscodeEncoderCandidates(executable, profile, transcodeMediaCodecCandidates(profile))
			if tc.wantErr {
				if !errors.Is(err, ErrNoVideoEncoder) {
					t.Fatalf("expected missing encoder error, got %v", err)
				}
				return
			}
			if err != nil || plan.filterTail != tc.want || plan.codec != tc.codec {
				t.Fatalf("selected %+v, error %v; want %s with %q", plan, err, tc.codec, tc.want)
			}
			if tc.codec == "libx264" {
				if plan.hardware {
					t.Fatalf("expected software fallback, got %+v", plan)
				}
				return
			}
			// The plan used for streaming must retain the device's accepted format.
			var command exec.Cmd
			var output bytes.Buffer
			err = runTranscodeWithFallback(context.Background(), &command, "movie.mkv", "movie.mkv", &output, plan, profile, "", func(p videoEncoderPlan, _ string) []string {
				return append([]string{executable, "-vf", joinVideoFilters("scale=640:360", p.filterTail)}, p.codecArgs...)
			}, nil)
			if err != nil || output.String() != "media" {
				t.Fatalf("stream with selected format: output %q, error %v", output.String(), err)
			}
		})
	}
}

func TestTranscodeMissingSoftwareEncoder(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tt := []struct {
		name  string
		serve func(context.Context, io.Writer, any, *exec.Cmd, *TranscodeOptions) error
	}{
		{"DLNA", ServeDLNATranscodedStream},
		{"Chromecast", ServeChromecastTranscodedStream},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GO2TV_TEST_FFMPEG_HELPER", "1")
			t.Setenv("GO2TV_TEST_FFMPEG_ENCODERS", "")
			var command exec.Cmd
			err := tc.serve(context.Background(), io.Discard, "movie.mkv", &command, &TranscodeOptions{FFmpegPath: executable})
			if !errors.Is(err, ErrNoVideoEncoder) || command.Process != nil {
				t.Fatalf("expected encoder error before streaming, got %v, process %v", err, command.Process)
			}
		})
	}
	// Hardware can pass its probe and still fail on the requested movie.
	t.Run("hardware fails at startup", func(t *testing.T) {
		t.Setenv("GO2TV_TEST_FFMPEG_HELPER", "1")
		t.Setenv("GO2TV_TEST_FFMPEG_ENCODERS", "h264_mediacodec")
		t.Setenv("GO2TV_TEST_FFMPEG_MODE", "encoder_failure")
		calls := filepath.Join(t.TempDir(), "calls.json")
		t.Setenv("GO2TV_TEST_FFMPEG_CALLS", calls)
		profile := videoEncoderProfileDLNA
		plan := transcodeHardwareEncoderPlan(profile, "h264_mediacodec", nil)
		var command exec.Cmd
		err := runTranscodeWithFallback(context.Background(), &command, "movie.mkv", "movie.mkv", io.Discard, plan, profile, "", func(p videoEncoderPlan, _ string) []string {
			return append([]string{executable}, p.codecArgs...)
		}, nil)
		data, readErr := os.ReadFile(calls)
		if !errors.Is(err, ErrNoVideoEncoder) || readErr != nil || bytes.Contains(data, []byte("libx264")) {
			t.Fatalf("expected missing encoder error without software attempt: %v, calls %s, read error %v", err, data, readErr)
		}
	})
}

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

func TestSelectTranscodeEncoderMissingExecutable(t *testing.T) {
	_, err := selectTranscodeVideoEncoder("/path/does/not/exist/ffmpeg", videoEncoderProfileChromecastFile)
	if err == nil {
		t.Fatalf("expected missing FFmpeg error")
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

	plan, err := selectTranscodeVideoEncoder(ffmpegPath, videoEncoderProfileChromecastFile)
	if err != nil {
		t.Fatalf("select encoder: %v", err)
	}
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

	plan, err := selectTranscodeVideoEncoder(ffmpegPath, videoEncoderProfileChromecastFile)
	if err != nil {
		t.Fatalf("select encoder: %v", err)
	}
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
  echo " V..... libx264             fake"
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
  echo  V..... libx264             fake
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
