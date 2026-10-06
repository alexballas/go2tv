package utils

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/image/font/gofont/gomono"
)

const styledBurnASS = `[Script Info]
ScriptType: v4.00+
PlayResX: 320
PlayResY: 180
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Sign,Go Mono,18,&H0000FF00,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,1.5,0,7,5,5,5,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 3,0:00:01.00,0:00:14.00,Sign,Actor,0,0,0,,{\move(10,25,100,25,0,13000)\fad(1000,1000)}Moving sign
Dialogue: 4,0:00:01.00,0:00:14.00,Sign,Actor,0,0,0,,{\an9\pos(310,10)\c&H0000FF&\b1\fs12}Right, smaller
Dialogue: 2,0:00:01.00,0:00:14.00,Sign,Actor,0,0,0,,{\an7\pos(10,80)\p1\c&HFF0000&}m 0 0 l 20 0 20 20 0 20
`

const styledBurnSSA = `[Script Info]
ScriptType: v4.00
PlayResX: 320
PlayResY: 180
[V4 Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, TertiaryColour, BackColour, Bold, Italic, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, AlphaLevel, Encoding
Style: Sign,Go Mono,18,65280,255,0,0,0,0,1,1.5,0,5,5,5,5,0,1
[Events]
Format: Marked, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: Marked=0,0:00:01.00,0:00:14.00,Sign,Actor,0,0,0,,{\move(10,25,100,25,0,13000)\fad(1000,1000)}Moving sign
Dialogue: Marked=0,0:00:01.00,0:00:14.00,Sign,Actor,0,0,0,,{\an9\pos(310,10)\c&H0000FF&\b1\fs12}Right, smaller
`

func styledSubtitleFixture(t *testing.T) (string, string, string) {
	t.Helper()
	return styledSubtitleScriptFixture(t, styledBurnASS, ".ass")
}

func styledSubtitleScriptFixture(t *testing.T, script, extension string) (string, string, string) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	if !ffmpegFilterAvailable(ffmpeg, "subtitles") {
		t.Skip("libass unavailable")
	}
	dir := t.TempDir()
	subs := filepath.Join(dir, "sign"+extension)
	font := filepath.Join(dir, "attached.ttf")
	for path, data := range map[string][]byte{subs: []byte(script), font: gomono.TTF} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	media := filepath.Join(dir, "styled.mkv")
	command := exec.Command(ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=size=320x180:rate=2:duration=15",
		"-i", subs, "-map", "0:v", "-map", "1:s", "-c:v", "mpeg4", "-g", "2", "-c:s", "copy", "-attach", font,
		"-metadata:s:t", "mimetype=application/x-truetype-font", media)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create styled fixture: %v: %s", err, output)
	}
	return ffmpeg, media, subs
}

type captionFileSource struct {
	path string
	size int64
}

func (s captionFileSource) Open(context.Context) (io.ReadSeekCloser, error) { return os.Open(s.path) }
func (s captionFileSource) URL() string                                     { return s.path }
func (s captionFileSource) MIME() string                                    { return "video/x-matroska" }
func (s captionFileSource) Size() int64                                     { return s.size }

// Compare rendered pixels against native libass, including attached font
// selection, simultaneous signs, drawings and animation after a window/seek.
func TestTorrentASSBurnMatchesOriginalRendering(t *testing.T) {
	tt := []struct{ name, script, extension string }{
		{"ASS", styledBurnASS, ".ass"}, {"SSA", styledBurnSSA, ".ssa"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			ffmpeg, media, _ := styledSubtitleScriptFixture(t, tc.script, tc.extension)
			checkTorrentStyledRendering(t, ffmpeg, media)
		})
	}
}

func checkTorrentStyledRendering(t *testing.T, ffmpeg, media string) {
	t.Helper()
	info, err := os.Stat(media)
	if err != nil {
		t.Fatal(err)
	}
	source := captionFileSource{path: media, size: info.Size()}
	tt := []struct {
		name        string
		seek, frame int
	}{
		{name: "before captions"},
		{name: "initial styling", seek: 2},
		{name: "seek at window boundary", seek: 10},
		{name: "seek during motion", seek: 11},
		{name: "expired captions", seek: 14},
		{name: "continuous playback across window boundary", frame: 250},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			opts := &TranscodeOptions{FFmpegPath: ffmpeg, TorrentSource: source, SeekSeconds: tc.seek}
			burn, err := prepareSubtitleBurn(ctx, opts)
			if err != nil {
				t.Fatal(err)
			}
			defer burn.cleanup()
			request := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-f", "image2pipe", "-framerate", "25", "-c:v", "png", "-probesize", "32", "-analyzeduration", "0", "-i", burn.overlay, "-vf", fmt.Sprintf("select='eq(n,%d)'", tc.frame), "-fps_mode", "passthrough", "-frames:v", "1", "-c:v", "png", "-f", "image2pipe", "pipe:1")
			actualBytes, err := request.Output()
			if err != nil {
				t.Fatalf("render caption window: %v", err)
			}
			actual, err := png.Decode(bytes.NewReader(actualBytes))
			if err != nil {
				t.Fatal(err)
			}
			at := float64(tc.seek) + float64(tc.frame)/25
			filter := fmt.Sprintf("setpts=PTS+%.9f/TB,subtitles='%s':si=0:alpha=1", at, escapeFFmpegPath(media))
			reference := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=black@0:size=320x180:rate=25:duration=1,format=rgba", "-vf", filter, "-frames:v", "1", "-c:v", "png", "-threads", "1", "-f", "image2pipe", "pipe:1")
			originalBytes, err := reference.Output()
			if err != nil {
				t.Fatalf("render reference: %v", err)
			}
			original, err := png.Decode(bytes.NewReader(originalBytes))
			if err != nil {
				t.Fatal(err)
			}
			if actual.Bounds() != original.Bounds() {
				t.Fatalf("canvas=%v want %v", actual.Bounds(), original.Bounds())
			}
			visible := false
			for y := range 180 {
				for x := range 320 {
					r, g, b, a := original.At(x, y).RGBA()
					visible = visible || a > 0
					ar, ag, ab, aa := actual.At(x, y).RGBA()
					if r != ar || g != ag || b != ab || a != aa {
						t.Fatalf("styling differs at (%d,%d): %v vs %v", x, y, actual.At(x, y), original.At(x, y))
					}
				}
			}
			if at > 0 && at < 14 && !visible {
				t.Fatal("reference unexpectedly lacks styled captions")
			}
		})
	}
}

func TestASSCaptionsConvertForReceiverPlayback(t *testing.T) {
	ffmpeg, _, path := styledSubtitleFixture(t)
	data, err := SubtitlesForPlayback(path, 10, ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("WEBVTT")) || !bytes.Contains(data, []byte("Moving sign")) || !bytes.Contains(data, []byte("00:00:00.000 --> 00:00:04.000")) {
		t.Fatalf("receiver captions missing or seek incorrect: %s", data)
	}
	if _, err := SubtitlesForPlayback(path, 0, filepath.Join(t.TempDir(), "missing-ffmpeg")); err == nil {
		t.Fatal("missing converter accepted")
	}
}

func TestLocalASSBurnPreservesTypesetting(t *testing.T) {
	ffmpeg, media, subs := styledSubtitleFixture(t)
	copied := filepath.Join(t.TempDir(), "copied.ass")
	if err := os.WriteFile(copied, []byte(styledBurnASS), 0600); err != nil {
		t.Fatal(err)
	}
	selected, err := EmbeddedSubtitleForBurn(ffmpeg, media, 0)
	if err != nil || selected == nil {
		t.Fatalf("select original ASS track: %+v, %v", selected, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tt := []struct {
		name string
		opts TranscodeOptions
	}{
		{name: "embedded with attached font", opts: TranscodeOptions{EmbeddedSubtitle: selected}},
		{name: "external with adjacent font", opts: TranscodeOptions{SubsPath: subs}},
		{name: "copied external retains original font directory", opts: TranscodeOptions{SubsPath: copied, SubtitleFontsDir: filepath.Dir(subs)}},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.FFmpegPath, tc.opts.SubtitleSize = ffmpeg, SubtitleSizeLarge
			burn, err := prepareSubtitleBurn(ctx, &tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			defer burn.cleanup()
			render := func(filter string) []byte {
				command := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=black:size=320x180:rate=25:duration=1",
					"-vf", "setpts=PTS+2/TB,"+filter, "-frames:v", "1", "-pix_fmt", "rgb24", "-f", "rawvideo", "pipe:1")
				data, err := command.Output()
				if err != nil {
					t.Fatalf("render local captions: %v", err)
				}
				return data
			}
			original := render(fmt.Sprintf("subtitles='%s':si=0", escapeFFmpegPath(media)))
			actual := render(burn.filter)
			if !bytes.Equal(actual, original) {
				t.Fatal("local burn changed original fonts, layout or style")
			}
		})
	}
}

func TestSubtitleFontsSkipAdjacentMedia(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "attached.TTF"), gomono.TTF, 0600); err != nil {
		t.Fatal(err)
	}
	sparseFile := func(name string, size int64) {
		t.Helper()
		file, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		err = file.Truncate(size)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("create sparse fixture: %v, %v", err, closeErr)
		}
	}
	sparseFile("large-video.mkv", 96<<20)
	assets, err := copySubtitleFonts(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(assets) })
	entries, err := os.ReadDir(assets)
	if err != nil || len(entries) != 1 {
		t.Fatalf("font staging included media: %v, %v", entries, err)
	}
	font, err := os.ReadFile(filepath.Join(assets, entries[0].Name()))
	if err != nil || !bytes.Equal(font, gomono.TTF) {
		t.Fatalf("staged font differs: %v", err)
	}
	sparseFile("z-oversized.ttf", maxSubtitleFontSize+1)
	failedAssets, err := copySubtitleFonts(context.Background(), dir)
	if err == nil {
		t.Fatal("oversized font accepted")
	}
	if _, err := os.Stat(failedAssets); !os.IsNotExist(err) {
		t.Fatalf("failed staging leaked assets: %v", err)
	}
}
