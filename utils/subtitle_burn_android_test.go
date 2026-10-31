//go:build android

package utils

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/text/encoding/charmap"
)

// Run on a device with scripts/test-android-subtitles.sh. Exercise the shipped
// libass/font stack, including default fonts and pre-API-28 subtitle encodings.
func TestAndroidSubtitleBurnPlayback(t *testing.T) {
	ffmpeg := os.Getenv("GO2TV_ANDROID_TEST_FFMPEG")
	if ffmpeg == "" {
		t.Skip("Android FFmpeg integration test requires GO2TV_ANDROID_TEST_FFMPEG")
	}
	t.Setenv("FONTCONFIG_FILE", "")
	dir := t.TempDir()
	media := filepath.Join(dir, "movie.mkv")
	command := exec.Command(ffmpeg, "-nostdin", "-v", "error",
		"-f", "lavfi", "-i", "color=size=320x180:rate=10:duration=4",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-c:v", "mpeg4", "-c:a", "aac", media)
	setSysProcAttr(command)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create Android media: %v: %s", err, output)
	}
	legacy, err := charmap.Windows1251.NewEncoder().Bytes([]byte("1\n00:00:00,000 --> 00:00:04,000\nПривет мир\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gomono.ttf"), gomono.TTF, 0600); err != nil {
		t.Fatal(err)
	}
	tt := []struct {
		name, extension string
		text            []byte
	}{
		{"system font", ".srt", []byte("1\n00:00:00,000 --> 00:00:04,000\nAndroid caption\n")},
		{"legacy encoding", ".srt", legacy},
		{"styled sidecar font", ".ass", []byte(styledBurnASS)},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			subs := filepath.Join(dir, "captions"+tc.extension)
			if err := os.WriteFile(subs, tc.text, 0600); err != nil {
				t.Fatal(err)
			}
			for _, renderer := range []struct {
				name, extension string
				serve           func(context.Context, io.Writer, any, *exec.Cmd, *TranscodeOptions) error
			}{
				{"DLNA", ".ts", ServeDLNATranscodedStream},
				{"Chromecast", ".mp4", ServeChromecastTranscodedStream},
			} {
				t.Run(renderer.name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
					defer cancel()
					var output, logs bytes.Buffer
					var ff exec.Cmd
					opts := &TranscodeOptions{FFmpegPath: ffmpeg, SubsPath: subs, SeekSeconds: 1, LogOutput: &logs}
					if err := renderer.serve(ctx, &output, media, &ff, opts); err != nil {
						t.Fatalf("burn Android subtitles: %v\n%s", err, &logs)
					}
					path := filepath.Join(t.TempDir(), "cast"+renderer.extension)
					if err := os.WriteFile(path, output.Bytes(), 0600); err != nil {
						t.Fatal(err)
					}
					decode := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-i", path, "-ss", "1",
						"-frames:v", "1", "-threads", "1", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1")
					setSysProcAttr(decode)
					frame, err := decode.Output()
					if err != nil || len(frame) != 320*180*3 {
						t.Fatalf("decode Android cast: bytes=%d, error=%v", len(frame), err)
					}
					visible := 0
					for _, pixel := range frame {
						if pixel > 80 {
							visible++
						}
					}
					if visible < 100 {
						t.Fatalf("Android captions missing: %d visible pixels\n%s", visible, &logs)
					}
				})
			}
		})
	}
}
