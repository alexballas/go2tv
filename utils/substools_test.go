package utils

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSubtitleNames(t *testing.T) {
	tt := []struct {
		name    string
		streams []streams
		want    []string
	}{
		{
			name: "single named subtitle keeps label",
			streams: []streams{
				{
					CodecType: "subtitle",
					Tags: map[string]string{
						"title":    "English",
						"language": "eng",
					},
				},
			},
			want: []string{"English (eng)"},
		},
		{
			name: "duplicate language-only labels are numbered",
			streams: []streams{
				{
					CodecType: "subtitle",
					Tags: map[string]string{
						"language": "eng",
					},
				},
				{
					CodecType: "subtitle",
					Tags: map[string]string{
						"language": "eng",
					},
				},
			},
			want: []string{"1", "2"},
		},
		{
			name: "different labels keep title and language",
			streams: []streams{
				{
					CodecType: "video",
				},
				{
					CodecType: "subtitle",
					Tags: map[string]string{
						"title":    "SDH",
						"language": "eng",
					},
				},
				{
					CodecType: "subtitle",
					Tags: map[string]string{
						"language": "el",
					},
				},
			},
			want: []string{"SDH (eng)", "el"},
		},
		{
			name: "missing labels use subtitle index",
			streams: []streams{
				{
					CodecType: "subtitle",
				},
				{
					CodecType: "subtitle",
				},
			},
			want: []string{"1", "2"},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got, err := subtitleNames(tc.streams)
			if err != nil {
				t.Fatalf("subtitleNames() error = %v", err)
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("subtitleNames() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// Exercises the same probe/extract path used by the embedded subtitle dropdown,
// including FFmpeg builds supplied by Flatpak runtimes.
func TestExtractSubRoundTrip(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	dir := t.TempDir()
	inputs := []string{"First track", "Second track"}
	args := []string{"-nostdin", "-loglevel", "error"}
	for i, text := range inputs {
		path := filepath.Join(dir, fmt.Sprintf("%d.srt", i))
		if err := os.WriteFile(path, []byte("1\n00:00:01,000 --> 00:00:02,000\n"+text+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-i", path)
	}
	media := filepath.Join(dir, "embedded.mkv")
	args = append(args, "-map", "0:s", "-map", "1:s", "-c:s", "srt", media)
	if output, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("create media: %v: %s", err, output)
	}
	names, err := GetSubs(ffmpeg, media)
	if err != nil || len(names) != len(inputs) {
		t.Fatalf("subtitle list = %v, error = %v", names, err)
	}
	for i, want := range inputs {
		t.Run(want, func(t *testing.T) {
			path, err := ExtractSub(ffmpeg, i, media)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(path)
			data, err := SubtitlesForPlayback(path, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), want) || !strings.Contains(string(data), "00:00:01.000 --> 00:00:02.000") {
				t.Fatalf("wrong track or timing: %s", data)
			}
		})
	}
	t.Run("invalid track reports FFmpeg error and cleans up", func(t *testing.T) {
		tempDir := t.TempDir()
		t.Setenv("TMPDIR", tempDir)
		t.Setenv("TMP", tempDir)
		path, err := ExtractSub(ffmpeg, 99, media)
		if path != "" || err == nil || !strings.Contains(err.Error(), "matches no streams") {
			t.Fatalf("path = %q, error = %v", path, err)
		}
		files, err := os.ReadDir(tempDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 0 {
			t.Fatalf("failed extraction leaked %d files", len(files))
		}
	})
}
