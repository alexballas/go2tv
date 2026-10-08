package utils

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"
	textunicode "golang.org/x/text/encoding/unicode"
)

func assConversionScript(dialogues ...string) string {
	return `[Script Info]
ScriptType: v4.00+
[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,24,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,1,0,2,10,10,10,1
[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
` + strings.Join(dialogues, "\n") + "\n"
}

func requireFFmpeg(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	return path
}

type notifyingPipeReader struct {
	*io.PipeReader
	started chan struct{}
}

func (r *notifyingPipeReader) Read(p []byte) (int, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	return r.PipeReader.Read(p)
}

func TestASSConversionPreservesLegacyAndUTF16Text(t *testing.T) {
	ffmpeg := requireFFmpeg(t)
	tests := []struct {
		name   string
		text   string
		encode func([]byte) ([]byte, error)
	}{
		{
			name: "short Windows-1252 cue",
			text: "Café",
			encode: func(data []byte) ([]byte, error) {
				return charmap.Windows1252.NewEncoder().Bytes(data)
			},
		},
		{
			name: "Windows-1252",
			text: "Café crème, déjà vu — très bientôt à Noël",
			encode: func(data []byte) ([]byte, error) {
				return charmap.Windows1252.NewEncoder().Bytes(data)
			},
		},
		{
			name: "Windows-1251",
			text: "Привет, как дела? Сегодня прекрасный день в городе",
			encode: func(data []byte) ([]byte, error) {
				return charmap.Windows1251.NewEncoder().Bytes(data)
			},
		},
		{
			name: "UTF-16 LE BOM",
			text: "こんにちは世界",
			encode: func(data []byte) ([]byte, error) {
				return textunicode.UTF16(textunicode.LittleEndian, textunicode.UseBOM).NewEncoder().Bytes(data)
			},
		},
		{
			name: "UTF-16 BE without BOM",
			text: "Γεια σου κόσμε",
			encode: func(data []byte) ([]byte, error) {
				return textunicode.UTF16(textunicode.BigEndian, textunicode.IgnoreBOM).NewEncoder().Bytes(data)
			},
		},
		{
			name: "UTF-16 LE without BOM and ASCII text",
			text: "Only ASCII text",
			encode: func(data []byte) ([]byte, error) {
				return textunicode.UTF16(textunicode.LittleEndian, textunicode.IgnoreBOM).NewEncoder().Bytes(data)
			},
		},
		{
			name: "UTF-8 BOM",
			text: "São Paulo",
			encode: func(data []byte) ([]byte, error) {
				return append([]byte{0xef, 0xbb, 0xbf}, data...), nil
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			script := assConversionScript("Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,," + tc.text)
			encoded, err := tc.encode([]byte(script))
			if err != nil {
				t.Fatal(err)
			}
			got, err := SubtitlesReaderForPlayback(bytes.NewReader(encoded), ".ass", 0, ffmpeg)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(got, []byte(tc.text)) || !bytes.Contains(got, []byte("00:01.000 --> 00:02.000")) {
				t.Fatalf("decoded cue or timing missing: %s", got)
			}
		})
	}
}

func TestASSConversionOmitsDrawingsAndKeepsText(t *testing.T) {
	ffmpeg := requireFFmpeg(t)
	script := assConversionScript(
		`Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,{\p1}m 0 0 l 100 100{\p0}`,
		`Dialogue: 0,0:00:03.00,0:00:04.00,Default,,0,0,0,,Before {\p1}m 0 0 l 100 100{\p0}after, with comma`,
		`Dialogue: 0,0:00:05.00,0:00:06.00,Default,,0,0,0,,{\pos(10,20)\pbo5}Plain text`,
	)
	got, err := SubtitlesReaderForPlayback(strings.NewReader(script), ".ass", 3, ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"00:00:00.000 --> 00:00:01.000", "Before after, with comma", "Plain text"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("%q missing: %s", want, got)
		}
	}
	if strings.Contains(string(got), "m 0 0") || strings.Contains(string(got), "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("drawing-only cue or coordinates leaked: %s", got)
	}

	srt, err := ConvertASSReaderToSRT(context.Background(), strings.NewReader(script), ffmpeg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(srt, []byte("Before after, with comma")) || bytes.Contains(srt, []byte("m 0 0")) {
		t.Fatalf("SRT drawing removal failed: %s", srt)
	}
}

func TestASSConversionCancellationClosesSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pipeReader, writer := io.Pipe()
	reader := &notifyingPipeReader{PipeReader: pipeReader, started: make(chan struct{})}
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		_, err := SubtitlesReaderForPlaybackContext(ctx, reader, ".ass", 0)
		done <- err
	}()
	select {
	case <-reader.started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("subtitle read did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subtitle read stayed blocked after cancellation")
	}
}
