package utils

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestParseVideoDuration(t *testing.T) {
	got := parseVideoDuration("Duration: 01:02:03.45, start: 0.000000")
	want := time.Hour + 2*time.Minute + 3*time.Second + 450*time.Millisecond
	if got != want {
		t.Fatalf("duration = %s, want %s", got, want)
	}
}

func TestExtractVideoThumbnailFromFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake skipped on windows")
	}
	dir := t.TempDir()
	framePath := filepath.Join(dir, "frame.jpg")
	frame, err := os.Create(framePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = jpeg.Encode(frame, solidThumbnailImage(40, 20), nil); err != nil {
		t.Fatal(err)
	}
	if err = frame.Close(); err != nil {
		t.Fatal(err)
	}
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\ncase \"$*\" in\n  *-protocols*) printf 'Input:\\n  fd\\nOutput:\\n'; exit 0 ;;\n  *-frames:v*) cat \"" + framePath + "\"; exit 0 ;;\nesac\nprintf 'Duration: 00:00:08.00\\n' >&2\nexit 1\n"
	if err = os.WriteFile(ffmpegPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	mediaPath := filepath.Join(dir, "movie.mp4")
	if err = os.WriteFile(mediaPath, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, err := os.Open(mediaPath)
	if err != nil {
		t.Fatal(err)
	}
	defer media.Close()

	data, err := ExtractVideoThumbnail(context.Background(), ffmpegPath, media)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || decoded.Width != 40 || decoded.Height != 20 {
		t.Fatalf("frame = %#v, err=%v", decoded, err)
	}
}

func solidThumbnailImage(width, height int) image.Image {
	result := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			result.Set(x, y, color.RGBA{R: 190, G: 80, A: 255})
		}
	}
	return result
}
