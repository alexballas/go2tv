package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedSubtitlePlaybackTiming(t *testing.T) {
	scripts := []struct{ name, script, extension string }{
		{"ASS", styledBurnASS, ".ass"}, {"SSA", styledBurnSSA, ".ssa"},
		{"SRT", "1\n00:00:01,000 --> 00:00:03,000\nPlain caption\n", ".srt"},
		{"WebVTT", "WEBVTT\n\n00:00:01.000 --> 00:00:03.000\nPlain caption\n", ".vtt"},
	}
	protocols := []struct {
		name, extension string
		serve           func(context.Context, io.Writer, any, *exec.Cmd, *TranscodeOptions) error
	}{
		{"DLNA", ".ts", ServeDLNATranscodedStream},
		{"Chromecast", ".mp4", ServeChromecastTranscodedStream},
	}
	for _, script := range scripts {
		t.Run(script.name, func(t *testing.T) {
			ffmpeg, base, _ := styledSubtitleScriptFixture(t, strings.ReplaceAll(script.script, "0:00:14.00", "0:00:03.00"), script.extension)
			ffprobe, err := ResolveFFprobePath(ffmpeg)
			if err != nil {
				t.Fatal(err)
			}
			for _, origin := range []int{0, 5} {
				t.Run(fmt.Sprintf("origin%d", origin), func(t *testing.T) {
					media := filepath.Join(t.TempDir(), "media.mkv")
					// Add audio and a higher frame rate to check both subtitle timing
					// and A/V synchronization in the actual playback pipelines.
					fixture := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-i", base,
						"-f", "lavfi", "-i", "sine=frequency=440:duration=15", "-map", "0:v", "-map", "1:a", "-map", "0:s", "-map", "0:t?",
						"-c:v", "mpeg4", "-r", "10", "-g", "10", "-c:a", "pcm_s16le", "-c:s", "copy", "-c:t", "copy", "-output_ts_offset", strconv.Itoa(origin), media)
					if output, err := fixture.CombinedOutput(); err != nil {
						t.Fatalf("create offset fixture: %v: %s", err, output)
					}
					selected, err := EmbeddedSubtitleForBurn(ffmpeg, media, 0)
					if err != nil || selected == nil {
						t.Fatalf("select embedded captions: %+v, %v", selected, err)
					}
					for _, protocol := range protocols {
						for _, seek := range []int{0, 2} {
							t.Run(fmt.Sprintf("%s/seek%d", protocol.name, seek), func(t *testing.T) {
								ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
								defer cancel()
								var output, logs bytes.Buffer
								var command exec.Cmd
								opts := &TranscodeOptions{FFmpegPath: ffmpeg, EmbeddedSubtitle: selected, SeekSeconds: seek, LogOutput: &logs}
								if err := protocol.serve(ctx, &output, media, &command, opts); err != nil {
									t.Fatalf("transcode: %v\n%s", err, &logs)
								}
								path := filepath.Join(t.TempDir(), "cast"+protocol.extension)
								if err := os.WriteFile(path, output.Bytes(), 0600); err != nil {
									t.Fatal(err)
								}
								at := []float64{0.5, 2, 7}
								if seek > 0 {
									at = []float64{0.5, 2, 5}
								}
								for _, seconds := range at {
									// Decode from the beginning: MPEG-TS seeking can skip
									// all frames in short fixtures with sparse keyframes.
									frame := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-i", path, "-ss", strconv.FormatFloat(seconds, 'f', -1, 64),
										"-frames:v", "1", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1")
									pixels, err := frame.Output()
									if err != nil || len(pixels) == 0 {
										t.Fatalf("frame at %vs: bytes=%d error=%v", seconds, len(pixels), err)
									}
									bright := 0
									for _, channel := range pixels {
										if channel > 50 {
											bright++
										}
									}
									want := seconds+float64(seek) >= 1 && seconds+float64(seek) < 3
									if visible := bright > 100; visible != want {
										t.Fatalf("captions at %vs: visible=%v want=%v", seconds, visible, want)
									}
								}
								assertSubtitlePlaybackTimeline(t, ctx, ffprobe, path, float64(15-seek))
							})
						}
					}
				})
			}
		})
	}
}

func assertSubtitlePlaybackTimeline(t *testing.T, ctx context.Context, ffprobe, path string, duration float64) {
	t.Helper()
	data, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "stream=codec_type,start_time:format=duration", "-of", "json", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			StartTime string `json:"start_time"`
		}
		Format struct{ Duration string }
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	starts := make(map[string]float64)
	for _, stream := range result.Streams {
		if stream.CodecType == "video" || stream.CodecType == "audio" {
			start, err := strconv.ParseFloat(stream.StartTime, 64)
			if err != nil {
				t.Fatal(err)
			}
			starts[stream.CodecType] = start
		}
	}
	if len(starts) != 2 || math.Abs(starts["video"]-starts["audio"]) > 0.35 {
		t.Fatalf("lost A/V synchronization: %+v", starts)
	}
	actualDuration, err := strconv.ParseFloat(result.Format.Duration, 64)
	if err != nil || math.Abs(actualDuration-duration) > 0.5 {
		t.Fatalf("playback duration = %v, want %v: %v", actualDuration, duration, err)
	}
}
