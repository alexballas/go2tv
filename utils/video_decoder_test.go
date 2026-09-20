package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/logging"
)

// Re-execute this test binary as FFmpeg: no shell or installed GPU required,
// including when these tests run natively on Windows.
func init() {
	if os.Getenv("GO2TV_TEST_FFMPEG_HELPER") != "1" {
		return
	}
	if slices.Contains(os.Args, "-hwaccels") {
		fmt.Println("Hardware acceleration methods:\nvideotoolbox\nd3d11va\ncuda\nvaapi")
		os.Exit(0)
	}
	f, err := os.OpenFile(os.Getenv("GO2TV_TEST_FFMPEG_CALLS"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		os.Exit(2)
	}
	if err := json.NewEncoder(f).Encode(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if err := f.Close(); err != nil {
		os.Exit(2)
	}
	hw := slices.Contains(os.Args, "-hwaccel")
	software := slices.Contains(os.Args, "libx264")
	switch os.Getenv("GO2TV_TEST_FFMPEG_MODE") {
	case "decoder_failure":
		if hw {
			fmt.Fprintln(os.Stderr, "decoder unavailable")
			os.Exit(1)
		}
	case "encoder_failure":
		if !software {
			fmt.Fprintln(os.Stderr, "GPU unavailable")
			os.Exit(1)
		}
	case "partial":
		fmt.Print("header")
		os.Exit(1)
	case "failure":
		os.Exit(1)
	case "cancel":
		for {
			time.Sleep(time.Hour)
		}
	}
	fmt.Print("media")
	os.Exit(0)
}

func TestTranscodeDownloadDecoder(t *testing.T) {
	tt := []struct{ os, codec, want string }{
		{"darwin", "h264_videotoolbox", "videotoolbox"},
		{"windows", "h264_amf", "d3d11va"},
		{"windows", "h264_qsv", "d3d11va"},
		{"windows", "h264_nvenc", ""}, // Keep the CUDA pipeline.
		{"linux", "h264_qsv", ""},
		{"android", "h264_mediacodec", ""},
		{"darwin", "libx264", ""},
	}
	for _, tc := range tt {
		t.Run(tc.os+"/"+tc.codec, func(t *testing.T) {
			if got := transcodeDownloadDecoder(tc.os, tc.codec); got != tc.want {
				t.Fatalf("decoder = %q, want %q", got, tc.want)
			}
			if tc.want == "" {
				return
			}
			args := transcodeInputArgs(transcodeHardwareEncoderPlan(videoEncoderProfileDLNA, tc.codec, nil), tc.want)
			if !slices.Equal(args, []string{"-hwaccel", tc.want}) {
				t.Fatalf("must download native frames for CPU filters, args = %q", args)
			}
		})
	}
}

func TestTranscodeDecoderExclusions(t *testing.T) {
	plan := transcodeHardwareEncoderPlan(videoEncoderProfileDLNA, "h264_nvenc", nil)
	path := "excluded-input-ffmpeg"
	cudaTranscodeCache.Store(path, true)
	t.Cleanup(func() { cudaTranscodeCache.Delete(path) })
	tt := []struct {
		name, filter, input string
		raw                 bool
	}{
		{"subtitles", "subtitles=movie.srt", "movie.mkv", false},
		{"pipe", "", "pipe:0", false},
		{"raw", "", "movie.mkv", true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectTranscodeVideoDecoder(path, plan, tc.filter, tc.input, tc.raw); got != "" {
				t.Fatalf("unexpected hardware decoder: %s", got)
			}
		})
	}
}

func TestTranscodeStartupFallback(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, hw := range []string{"videotoolbox", "d3d11va", "cuda", "vaapi", "drm-pi4", "drm-pi5"} {
		for _, profile := range []videoEncoderProfile{videoEncoderProfileDLNA, videoEncoderProfileChromecastFile} {
			tt := []struct {
				name, mode, input string
				attempts          int
				wantErr           bool
				output            string
			}{
				{"success", "", "movie.mkv", 1, false, "media"},
				{"decoder fails", "decoder_failure", "movie.mkv", 2, false, "media"},
				{"encoder also fails", "encoder_failure", "movie.mkv", 3, false, "media"},
				{"partial output", "partial", "movie.mkv", 1, true, "header"},
				{"all fail", "failure", "movie.mkv", 3, true, ""},
				{"pipe cannot retry", "failure", "pipe:0", 1, true, ""},
				{"cancelled startup", "cancel", "movie.mkv", 1, true, ""},
				{"already cancelled", "pre_cancel", "movie.mkv", 0, true, ""},
			}
			for _, tc := range tt {
				t.Run(hw+"/"+string(profile)+"/"+tc.name, func(t *testing.T) {
					callsPath := filepath.Join(t.TempDir(), "calls.json")
					t.Setenv("GO2TV_TEST_FFMPEG_HELPER", "1")
					t.Setenv("GO2TV_TEST_FFMPEG_CALLS", callsPath)
					t.Setenv("GO2TV_TEST_FFMPEG_MODE", tc.mode)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if tc.mode == "pre_cancel" {
						cancel()
					}
					if tc.mode == "cancel" {
						go func() {
							ticker := time.NewTicker(time.Millisecond)
							defer ticker.Stop()
							for {
								select {
								case <-ctx.Done():
									return
								case <-ticker.C:
									if data, err := os.ReadFile(callsPath); err == nil && bytes.Contains(data, []byte("\n")) {
										cancel()
										return
									}
								}
							}
						}()
					}
					codec := map[string]string{"videotoolbox": "h264_videotoolbox", "d3d11va": "h264_qsv", "cuda": "h264_nvenc", "vaapi": "h264_vaapi", "drm-pi4": "h264_v4l2m2m", "drm-pi5": "libx264"}[hw]
					plan := transcodeHardwareEncoderPlan(profile, codec, nil)
					decoder := hw
					if strings.HasPrefix(hw, "drm-") {
						decoder = "drm"
					}
					if hw == "drm-pi5" {
						plan = transcodeSoftwareEncoderPlan(profile)
						switch tc.mode {
						case "encoder_failure":
							tc.attempts = 1
						case "failure":
							if tc.input != "pipe:0" {
								tc.attempts = 2
							}
						}
					}
					build := func(p videoEncoderPlan, decoder string) []string {
						args := append([]string{executable}, transcodeInputArgs(p, decoder)...)
						args = append(args, "-ss", "37", "-copyts", "-i", tc.input)
						return append(args, p.codecArgs...)
					}
					var out, logs bytes.Buffer
					var command exec.Cmd
					var input any = tc.input
					if tc.input == "pipe:0" {
						input = strings.NewReader("input")
					}
					err := runTranscodeWithFallback(ctx, &command, input, tc.input, &out, plan, profile, decoder, build, logging.NewJSON(&logs))
					if (err != nil) != tc.wantErr {
						t.Fatalf("error = %v, want error %v", err, tc.wantErr)
					}
					if strings.Contains(tc.mode, "cancel") && !errors.Is(err, context.Canceled) {
						t.Fatalf("expected cancellation, got %v", err)
					}
					if out.String() != tc.output {
						t.Fatalf("output = %q, want %q", out.String(), tc.output)
					}
					data, readErr := os.ReadFile(callsPath)
					if readErr != nil && !os.IsNotExist(readErr) {
						t.Fatal(readErr)
					}
					var calls [][]string
					dec := json.NewDecoder(bytes.NewReader(data))
					for {
						var args []string
						err := dec.Decode(&args)
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						calls = append(calls, args)
					}
					if len(calls) != tc.attempts {
						t.Fatalf("attempts = %d, want %d: %s", len(calls), tc.attempts, logs.String())
					}
					for i, args := range calls {
						if slices.Contains(args, "-hwaccel") != (i == 0) {
							t.Fatalf("attempt %d retained wrong decoder: %q", i, args)
						}
						if slices.Contains(args, "libx264") != (i == 2 || hw == "drm-pi5") {
							t.Fatalf("attempt %d wrong encoder: %q", i, args)
						}
					}
					logDecoder := json.NewDecoder(&logs)
					attempts, fallbacks := 0, 0
					for {
						var record struct {
							Message          string   `json:"msg"`
							Args             []string `json:"args"`
							RequestedDecoder string   `json:"requested_decoder"`
							FailedDecoder    string   `json:"failed_decoder"`
							FailedEncoder    string   `json:"failed_encoder"`
							NextAction       string   `json:"next_action"`
							NextEncoder      string   `json:"next_encoder"`
						}
						err := logDecoder.Decode(&record)
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						wantDecoder := "software"
						switch record.Message {
						case "transcode attempt":
							if attempts == 0 {
								wantDecoder = decoder
							}
							if attempts >= len(calls) || !slices.Equal(record.Args, append([]string{executable}, calls[attempts]...)) || record.RequestedDecoder != wantDecoder {
								t.Fatalf("incorrect attempt diagnostic: %+v", record)
							}
							attempts++
						case "transcode startup fallback":
							wantAction, wantEncoder := "software_encode", "libx264"
							if fallbacks == 0 {
								wantDecoder, wantAction, wantEncoder = decoder, "software_decode", codec
							}
							if record.FailedDecoder != wantDecoder || record.FailedEncoder != codec || record.NextAction != wantAction || record.NextEncoder != wantEncoder {
								t.Fatalf("incorrect fallback diagnostic: %+v", record)
							}
							fallbacks++
						}
					}
					if attempts != len(calls) || fallbacks != max(0, len(calls)-1) {
						t.Fatalf("logged %d attempts, %d fallbacks for %d calls", attempts, fallbacks, len(calls))
					}
				})
			}
		}
	}
}

func TestTranscodeAttemptLoggingLevel(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tt := []struct {
		name  string
		level slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GO2TV_TEST_FFMPEG_HELPER", "1")
			t.Setenv("GO2TV_TEST_FFMPEG_CALLS", filepath.Join(t.TempDir(), "calls.json"))
			t.Setenv("GO2TV_TEST_FFMPEG_MODE", "")
			var logs, out bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: tc.level}))
			var command exec.Cmd
			plan := transcodeSoftwareEncoderPlan(videoEncoderProfileDLNA)
			args := []string{executable, "-i", "movie with spaces.mkv", "-c:v", "libx264"}
			err := runTranscodeWithFallback(context.Background(), &command, "movie with spaces.mkv", "movie with spaces.mkv", &out, plan, videoEncoderProfileDLNA, "", func(videoEncoderPlan, string) []string { return args }, logger)
			if err != nil || out.String() != "media" {
				t.Fatalf("transcode: output %q, error %v", out.String(), err)
			}
			if tc.level == slog.LevelInfo {
				if logs.Len() != 0 {
					t.Fatalf("debug disabled: unexpected logs %s", &logs)
				}
				return
			}
			var record struct {
				Args []string `json:"args"`
			}
			if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(record.Args, args) {
				t.Fatalf("logged args = %q, want %q", record.Args, args)
			}
		})
	}
}

// Exercise the public streaming entry points on each host OS. The helper tests
// above cover every backend on every OS; these check the final FFmpeg commands.
func TestServeTranscodeDecoderFallback(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	codec, hw := "h264_nvenc", "cuda"
	switch runtime.GOOS {
	case "darwin":
		codec, hw = "h264_videotoolbox", "videotoolbox"
	case "windows":
		codec, hw = "h264_amf", "d3d11va"
	}
	for _, profile := range []videoEncoderProfile{videoEncoderProfileDLNA, videoEncoderProfileChromecastFile} {
		t.Run(string(profile), func(t *testing.T) {
			key := transcodeEncoderCacheKey(executable, profile)
			transcodeVideoEncoderCache.Store(key, transcodeHardwareEncoderPlan(profile, codec, nil))
			cudaTranscodeCache.Store(executable, true)
			t.Cleanup(func() {
				transcodeVideoEncoderCache.Delete(key)
				cudaTranscodeCache.Delete(executable)
				ffmpegFilterCache.Delete(executable + "|hwaccel|" + hw)
			})
			path := filepath.Join(t.TempDir(), "calls.json")
			t.Setenv("GO2TV_TEST_FFMPEG_HELPER", "1")
			t.Setenv("GO2TV_TEST_FFMPEG_CALLS", path)
			t.Setenv("GO2TV_TEST_FFMPEG_MODE", "decoder_failure")
			var out, logs bytes.Buffer
			var command exec.Cmd
			if profile == videoEncoderProfileDLNA {
				err = ServeTranscodedStream(context.Background(), &out, "movie.mkv", &command, executable, "", 37, SubtitleSizeMedium, logging.NewJSON(&logs))
			} else {
				err = ServeChromecastTranscodedStream(context.Background(), &out, "movie.mkv", &command, &TranscodeOptions{FFmpegPath: executable, SeekSeconds: 37, LogOutput: &logs})
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.String() != "media" {
				t.Fatalf("output = %q", out.String())
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			dec := json.NewDecoder(bytes.NewReader(data))
			for i := range 2 {
				var args []string
				if err := dec.Decode(&args); err != nil {
					t.Fatal(err)
				}
				seek, input := slices.Index(args, "-ss"), slices.Index(args, "-i")
				if seek < 0 || seek >= input || args[seek+1] != "37" || !slices.Contains(args, "-copyts") {
					t.Fatalf("lost seek on attempt %d: %q", i, args)
				}
				accel := slices.Index(args, "-hwaccel")
				if i == 0 && (accel < 0 || accel >= input || args[accel+1] != hw) {
					t.Fatalf("missing input decoder: %q", args)
				}
				if i == 1 && accel >= 0 {
					t.Fatalf("fallback retained hardware decode: %q", args)
				}
				if !slices.Contains(args, codec) {
					t.Fatalf("fallback dropped hardware encoding: %q", args)
				}
				filter := slices.Index(args, "-vf")
				if filter < 0 || !strings.Contains(args[filter+1], "min(1920,iw)") {
					t.Fatalf("missing bounded scaling: %q", args)
				}
				if (hw == "d3d11va" || hw == "videotoolbox") && slices.Contains(args, "-hwaccel_output_format") {
					t.Fatalf("CPU filters require downloaded frames: %q", args)
				}
			}
			var extra []string
			if err := dec.Decode(&extra); err != io.EOF {
				t.Fatalf("unexpected further attempt: %q, %v", extra, err)
			}
			if !strings.Contains(logs.String(), "decoder unavailable") {
				t.Fatalf("missing fallback reason: %s", logs.String())
			}
		})
	}
}

func TestRaspberryPiTranscodeAvailability(t *testing.T) {
	tt := []struct {
		name, driver string
		drm          bool
		want         bool
	}{
		{"no video device", "", true, false},
		{"unrelated video device", "bcm2835-isp", true, false},
		{"Pi HEVC decoder", "rpi-hevc-dec\n", true, true},
		{"older Pi kernel", "rpivid\n", true, true},
		{"FFmpeg lacks DRM", "rpi-hevc-dec\n", false, false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.driver != "" {
				node := filepath.Join(dir, "video19")
				if err := os.Mkdir(node, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(node, "name"), []byte(tc.driver), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ffmpeg := filepath.Join(dir, "ffmpeg")
			key := ffmpeg + "|hwaccel|drm"
			ffmpegFilterCache.Store(key, tc.drm)
			t.Cleanup(func() { ffmpegFilterCache.Delete(key) })
			if got := raspberryPiTranscodeAvailable(ffmpeg, dir); got != tc.want {
				t.Fatalf("available = %v, want %v", got, tc.want)
			}
		})
	}
}
