package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubtitlesForPlayback(t *testing.T) {
	tt := []struct {
		name, ext, input, want string
		seek                   int
	}{
		{name: "native SRT", ext: ".srt", input: "1\n00:00:08,000 --> 00:00:12,000\nHello\n", want: "WEBVTT\n\n1\n00:00:08.000 --> 00:00:12.000\nHello\n"},
		{name: "seek removes expired cues and keeps overlapping cue", ext: ".srt", seek: 10, input: "1\n00:00:01,000 --> 00:00:10,000\nExpired\n\n2\n00:00:08,000 --> 00:00:12,000\nOverlapping\n\n3\n00:00:15,250 --> 00:00:18,500\nLater\n", want: "WEBVTT\n\n2\n00:00:00.000 --> 00:00:02.000\nOverlapping\n\n3\n00:00:05.250 --> 00:00:08.500\nLater\n"},
		{name: "VTT settings and styles survive seek", ext: ".vtt", seek: 60, input: "WEBVTT\r\n\r\nSTYLE\r\n::cue { color: lime; }\r\n\r\ncaption\r\n01:01.250 --> 01:03.500 align:start\r\nHello\r\n", want: "WEBVTT\n\nSTYLE\n::cue { color: lime; }\n\ncaption\n00:00:01.250 --> 00:00:03.500 align:start\nHello\n"},
		{name: "native VTT untouched", ext: ".vtt", input: "WEBVTT\n\n00:01.000 --> 00:02.000\nHello\n", want: "WEBVTT\n\n00:01.000 --> 00:02.000\nHello\n"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "subtitles"+tc.ext)
			if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := SubtitlesForPlayback(path, tc.seek)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("missing file reports error", func(t *testing.T) {
		if _, err := SubtitlesForPlayback(filepath.Join(t.TempDir(), "missing.srt"), 0); err == nil {
			t.Fatal("missing file accepted")
		}
	})
	t.Run("unsupported format reports error", func(t *testing.T) {
		if _, err := SubtitlesForPlayback("captions.sup", 0); err == nil || !strings.Contains(err.Error(), "unsupported") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestSubtitlesForPlaybackStripsASSOverrides(t *testing.T) {
	tt := []struct {
		name, ext, input string
		wantContains     []string
		wantNotContains  []string
	}{
		{
			name:            "SRT an8 becomes VTT settings",
			ext:             ".srt",
			input:           "1\n00:00:01,000 --> 00:00:04,000\n{\\an8}<i>Italic</i> top\n",
			wantContains:    []string{"line:1 align:center", "<i>Italic</i> top"},
			wantNotContains: []string{`{\`},
		},
		{
			name:            "VTT an1 becomes settings",
			ext:             ".vtt",
			input:           "WEBVTT\n\n00:01.000 --> 00:04.000\n{\\an1}bottom left\n",
			wantContains:    []string{"align:left", "bottom left"},
			wantNotContains: []string{`{\`, "line:100%"},
		},
		{
			name:            "SRT bold maps to html",
			ext:             ".srt",
			input:           "1\n00:00:01,000 --> 00:00:04,000\n{\\b1}Bold{\\b0}\n",
			wantContains:    []string{"<b>Bold</b>"},
			wantNotContains: []string{`{\`},
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "subtitles"+tc.ext)
			if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := SubtitlesForPlayback(path, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(string(got), want) {
					t.Fatalf("missing %q in %q", want, got)
				}
			}
			for _, bad := range tc.wantNotContains {
				if strings.Contains(string(got), bad) {
					t.Fatalf("unexpected %q in %q", bad, got)
				}
			}
		})
	}
}

func TestSubtitlesForPlaybackSeekKeepsInjectedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subtitles.srt")
	input := "1\n00:00:15,000 --> 00:00:20,000\n{\\an8}Top after seek\n"
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := SubtitlesForPlayback(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"00:00:05.000 --> 00:00:10.000 line:1 align:center", "Top after seek"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(string(got), `{\`) {
		t.Fatalf("override leaked: %q", got)
	}
}

func TestASSToWebVTTInjectsAlignment(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	// Style Alignment=2 (default) keeps inline {\anN} in FFmpeg SRT output.
	script := `[Script Info]
ScriptType: v4.00+
[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,24,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,1,0,2,10,10,10,1
[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:05.00,0:00:08.00,Default,,0,0,0,,{\an1}Bottom left
Dialogue: 0,0:00:01.00,0:00:04.00,Default,,0,0,0,,{\an8}Top line
`
	got, err := SubtitlesReaderForPlayback(strings.NewReader(script), ".ass", 0, ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"line:1 align:center",
		"Top line",
		"align:left",
		"Bottom left",
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(string(got), `{\`) {
		t.Fatalf("override leaked: %q", got)
	}
}
