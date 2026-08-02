//go:build !(android || ios)

package gui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"go2tv.app/screencast/capture"
)

func TestDLNARawFrameSize(t *testing.T) {
	tt := []struct {
		name        string
		width       uint32
		height      uint32
		pixelFormat string
		want        int
		wantErr     bool
	}{
		{name: "bgra 1080p", width: 1920, height: 1080, pixelFormat: "", want: 1920 * 1080 * 4},
		{name: "bgra explicit", width: 1280, height: 720, pixelFormat: capture.PixelFormatBGRA, want: 1280 * 720 * 4},
		{name: "zero width", width: 0, height: 720, pixelFormat: capture.PixelFormatBGRA, wantErr: true},
		{name: "zero height", width: 1280, height: 0, pixelFormat: capture.PixelFormatBGRA, wantErr: true},
		{name: "unsupported format", width: 1280, height: 720, pixelFormat: "NV12", wantErr: true},
		{name: "overflow", width: 1 << 31, height: 1 << 30, pixelFormat: capture.PixelFormatBGRA, wantErr: true},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dlnaRawFrameSize(tc.width, tc.height, tc.pixelFormat)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDLNAScreencastTargetFPS(t *testing.T) {
	tt := []struct {
		name   string
		stream *capture.Stream
		want   uint32
	}{
		{name: "zero rate defaults", stream: &capture.Stream{FrameRate: 0, Width: 1920, Height: 1080}, want: dlnaScreencastMaxFrameRate},
		{name: "under cap", stream: &capture.Stream{FrameRate: 30, Width: 1920, Height: 1080}, want: 30},
		{name: "over cap", stream: &capture.Stream{FrameRate: 120, Width: 1920, Height: 1080}, want: dlnaScreencastMaxFrameRate},
		{name: "hi res caps fps", stream: &capture.Stream{FrameRate: 60, Width: 3840, Height: 2160}, want: dlnaScreencastHighResCapFPS},
		{name: "hi res under cap", stream: &capture.Stream{FrameRate: 24, Width: 3840, Height: 2160}, want: 24},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := dlnaScreencastTargetFPS(tc.stream); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCopyToConsuming(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("abcdef")

	dst := make([]byte, 4)
	n := copyToConsuming(dst, &buf)
	if n != 4 || string(dst) != "abcd" {
		t.Fatalf("first copy got n=%d dst=%q", n, dst)
	}

	n = copyToConsuming(dst, &buf)
	if n != 2 || string(dst[:2]) != "ef" {
		t.Fatalf("second copy got n=%d dst=%q", n, dst)
	}

	if n := copyToConsuming(dst, &buf); n != 0 {
		t.Fatalf("empty buffer copy got n=%d", n)
	}
}

func TestLockedBufferTail(t *testing.T) {
	b := &lockedBuffer{}
	_, _ = b.Write([]byte("line1\nline2\nline3"))

	if got := b.Tail(5); got != "line3" {
		t.Fatalf("tail got %q", got)
	}
	if got := b.Tail(100); got != "line1\nline2\nline3" {
		t.Fatalf("full tail got %q", got)
	}

	empty := &lockedBuffer{}
	if got := empty.Tail(10); got == "" {
		t.Fatalf("expected fallback message, got empty")
	}
}

func TestDLNASilenceReader(t *testing.T) {
	r := newDLNASilenceReader(48000, 2, 16, 20*time.Millisecond)

	buf := make([]byte, 3840)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read error: %s", err)
	}
	if n != len(buf) {
		t.Fatalf("got %d bytes, want %d", n, len(buf))
	}
	for _, b := range buf {
		if b != 0 {
			t.Fatalf("expected silence, got %02x", b)
		}
	}

	if err := r.Close(); err != nil {
		t.Fatalf("close error: %s", err)
	}
	if _, err := r.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF after close, got %v", err)
	}
}

func TestTSStreamProbe(t *testing.T) {
	p := newTSStreamProbe()
	src := strings.NewReader(strings.Repeat("x", dlnaScreencastStartupBytes))

	go p.run(src)

	select {
	case <-p.ready:
	case <-time.After(5 * time.Second):
		t.Fatalf("probe never became ready")
	}

	if got := p.Buffered(); got < dlnaScreencastStartupBytes {
		t.Fatalf("buffered %d, want >= %d", got, dlnaScreencastStartupBytes)
	}
}

func TestDLNATSPrefixReader(t *testing.T) {
	payload := bytes.Repeat([]byte("abcdefgh"), 512)
	src := bytes.NewReader(payload)

	p := newTSStreamProbe()
	go p.run(src)

	select {
	case <-p.ready:
	case <-time.After(5 * time.Second):
		t.Fatalf("probe never became ready")
	}

	r := &dlnaTSPrefixReader{probe: p, src: io.NopCloser(bytes.NewReader(payload))}

	var out bytes.Buffer
	buf := make([]byte, 64)
	for {
		n, err := r.Read(buf)
		out.Write(buf[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read error: %s", err)
		}
	}

	if out.Len() != 2*len(payload) {
		t.Fatalf("output length %d, want %d", out.Len(), 2*len(payload))
	}
}

func TestDLNAFramePacer(t *testing.T) {
	frameSize := 16 * 16 * 4
	payload := bytes.Repeat([]byte{1}, frameSize*2)

	src := io.NopCloser(bytes.NewReader(payload))
	p, err := newDLNAFramePacer(&capture.Stream{
		ReadCloser:  src,
		Width:       16,
		Height:      16,
		FrameRate:   30,
		PixelFormat: capture.PixelFormatBGRA,
	}, 30)
	if err != nil {
		t.Fatalf("pacer error: %s", err)
	}
	defer p.Close()

	buf := make([]byte, frameSize)
	start := time.Now()
	if _, err := io.ReadFull(p, buf); err != nil {
		t.Fatalf("read error: %s", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("first frame took too long: %s", time.Since(start))
	}
}

func TestDLNAFramePacerNilStream(t *testing.T) {
	_, err := newDLNAFramePacer(nil, 30)
	if err == nil {
		t.Fatalf("expected error for nil stream")
	}
}
