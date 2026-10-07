package utils

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A white 16x16 PGS object on a 320x180 canvas, visible from seconds 1 to 5.
func bitmapSubtitleFixture(t *testing.T) (string, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	var sup []byte
	segment := func(at uint32, kind byte, payload []byte) {
		sup = append(sup, 'P', 'G')
		sup = binary.BigEndian.AppendUint32(sup, at*90000)
		sup = binary.BigEndian.AppendUint32(sup, 0)
		sup = append(sup, kind)
		sup = binary.BigEndian.AppendUint16(sup, uint16(len(payload)))
		sup = append(sup, payload...)
	}
	segment(1, 0x16, []byte{1, 64, 0, 180, 0x10, 0, 0, 0x80, 0, 0, 1, 0, 0, 0, 0, 0, 32, 0, 32})
	segment(1, 0x17, []byte{1, 0, 0, 0, 0, 0, 1, 64, 0, 180})
	segment(1, 0x14, []byte{0, 0, 0, 16, 128, 128, 0, 1, 235, 128, 128, 255})
	object := []byte{0, 16, 0, 16}
	for range 16 {
		object = append(object, bytes.Repeat([]byte{1}, 16)...)
		object = append(object, 0, 0)
	}
	length := len(object)
	segment(1, 0x15, append([]byte{0, 0, 0, 0xc0, byte(length >> 16), byte(length >> 8), byte(length)}, object...))
	segment(1, 0x80, nil)
	segment(5, 0x16, []byte{1, 64, 0, 180, 0x10, 0, 1, 0, 0, 0, 0})
	segment(5, 0x80, nil)
	path := filepath.Join(dir, "captions.sup")
	if err := os.WriteFile(path, sup, 0600); err != nil {
		t.Fatal(err)
	}
	text := filepath.Join(dir, "captions.srt")
	if err := os.WriteFile(text, []byte("1\n00:00:00,000 --> 00:00:06,000\nText caption\n"), 0600); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "movie.mkv")
	command := exec.Command(ffmpeg, "-nostdin", "-v", "error",
		"-f", "lavfi", "-i", "color=size=160x90:duration=6",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6", "-i", text, "-i", path,
		"-map", "0:v", "-map", "1:a", "-map", "2:s", "-map", "3:s",
		"-c:v", "mpeg4", "-c:a", "aac", "-c:s", "copy", media)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create bitmap media: %v: %s", err, output)
	}
	return ffmpeg, media
}

func TestBitmapSubtitleExtractionReportsUnsupportedText(t *testing.T) {
	ffmpeg, media := bitmapSubtitleFixture(t)
	for _, input := range []string{media, filepath.Join(filepath.Dir(media), "captions.sup")} {
		t.Run(filepath.Ext(input), func(t *testing.T) {
			track := 0
			if input == media {
				track = 1
			}
			tempDir := t.TempDir()
			t.Setenv("TMPDIR", tempDir)
			t.Setenv("TMP", tempDir)
			path, err := ExtractSub(ffmpeg, track, input)
			if path != "" || !errors.Is(err, ErrBitmapSubtitles) {
				t.Fatalf("bitmap extraction: path=%q error=%v", path, err)
			}
			files, err := os.ReadDir(tempDir)
			if err != nil || len(files) != 0 {
				t.Fatalf("extraction left temporary files: %v, %v", files, err)
			}
		})
	}
}

func TestBitmapSubtitleBurnPlayback(t *testing.T) {
	ffmpeg, media := bitmapSubtitleFixture(t)
	selected, err := EmbeddedSubtitleForBurn(ffmpeg, media, 1)
	if err != nil || selected == nil || !selected.Bitmap || selected.Track != 1 {
		t.Fatalf("select bitmap track: %+v, %v", selected, err)
	}
	tt := []struct {
		name, extension string
		serve           func(context.Context, io.Writer, any, *exec.Cmd, *TranscodeOptions) error
	}{
		{"DLNA", ".ts", ServeDLNATranscodedStream},
		{"Chromecast", ".mp4", ServeChromecastTranscodedStream},
	}
	for _, tc := range tt {
		for _, seek := range []int{0, 2} {
			t.Run(fmt.Sprintf("%s/seek%d", tc.name, seek), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				var output, logs bytes.Buffer
				opts := &TranscodeOptions{FFmpegPath: ffmpeg, EmbeddedSubtitle: selected, SeekSeconds: seek, LogOutput: &logs}
				var command exec.Cmd
				if err := tc.serve(ctx, &output, media, &command, opts); err != nil {
					t.Fatalf("transcode bitmap: %v\n%s", err, &logs)
				}
				path := filepath.Join(t.TempDir(), "cast"+tc.extension)
				if err := os.WriteFile(path, output.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
				probe, err := ResolveFFprobePath(ffmpeg)
				if err != nil {
					t.Fatal(err)
				}
				streams, err := exec.CommandContext(ctx, probe, "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", path).Output()
				if err != nil || !bytes.Contains(streams, []byte("video")) || !bytes.Contains(streams, []byte("audio")) {
					t.Fatalf("cast lost video/audio: %s, %v", streams, err)
				}
				for _, at := range []float64{0, 2, 5.5 - float64(seek)} {
					decode := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-i", path, "-ss", fmt.Sprint(at),
						"-frames:v", "1", "-threads", "1", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1")
					frame, err := decode.Output()
					if err != nil || len(frame) != 160*90*3 {
						t.Fatalf("decode at %.1f: bytes=%d error=%v", at, len(frame), err)
					}
					// The PGS canvas is twice the video's dimensions. The white
					// object must land at (16,16), not be clipped or shifted.
					pixel := frame[(18*160+18)*3]
					visible := at+float64(seek) >= 1 && at+float64(seek) < 5
					if (pixel > 200) != visible || frame[(40*160+40)*3] > 30 {
						t.Fatalf("bitmap timing/scale at %.1f (seek %d): pixel=%d want visible=%v\n%s", at, seek, pixel, visible, &logs)
					}
				}
			})
		}
	}
}
