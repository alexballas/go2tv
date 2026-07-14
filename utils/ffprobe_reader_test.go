package utils

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type memoryReadSeekCloser struct{ *bytes.Reader }

func (*memoryReadSeekCloser) Close() error { return nil }

func writeDurationProbeTools(t *testing.T) (ffmpeg, argsFile, stdinFile string) {
	t.Helper()
	dir := t.TempDir()
	ffmpeg = filepath.Join(dir, "ffmpeg")
	ffprobe := filepath.Join(dir, "ffprobe")
	argsFile = filepath.Join(dir, "args")
	stdinFile = filepath.Join(dir, "stdin")

	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
if [ "$GO2TV_PROBE_BLOCK" = "1" ]; then
	while :; do :; done
fi
printf '%s\n' "$@" > "$GO2TV_PROBE_ARGS"
cat > "$GO2TV_PROBE_STDIN"
printf '{"format":{"duration":"12.75"}}'
`
	if err := os.WriteFile(ffprobe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO2TV_PROBE_ARGS", argsFile)
	t.Setenv("GO2TV_PROBE_STDIN", stdinFile)
	return ffmpeg, argsFile, stdinFile
}

func probeInputURL(t *testing.T, argsFile string) string {
	t.Helper()
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(string(data))
	if len(args) == 0 {
		t.Fatal("ffprobe arguments missing")
	}
	return args[len(args)-1]
}

func TestDurationForMediaReaderSecondsFileFD(t *testing.T) {
	ffmpeg, argsFile, stdinFile := writeDurationProbeTools(t)
	ffmpegProtocolCache.Store(ffmpeg+"|fd", true)
	t.Cleanup(func() { ffmpegProtocolCache.Delete(ffmpeg + "|fd") })

	const contents = "seekable media"
	file, err := os.Create(filepath.Join(t.TempDir(), "media.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(contents); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(4, io.SeekStart); err != nil {
		t.Fatal(err)
	}

	duration, err := DurationForMediaReaderSeconds(context.Background(), ffmpeg, file)
	if err != nil {
		t.Fatal(err)
	}
	if duration != 12.75 {
		t.Fatalf("duration = %v", duration)
	}
	if offset, err := file.Seek(0, io.SeekCurrent); err != nil || offset != 4 {
		t.Fatalf("input offset = %d, err %v", offset, err)
	}
	if stdin, err := os.ReadFile(stdinFile); err != nil || string(stdin) != contents {
		t.Fatalf("ffprobe stdin = %q, err %v", stdin, err)
	}
	if got := probeInputURL(t, argsFile); got != "fd:" {
		t.Fatalf("ffprobe input = %q", got)
	}
}

func TestDurationForMediaReaderSecondsPipeFallback(t *testing.T) {
	t.Run("reader", func(t *testing.T) {
		ffmpeg, argsFile, stdinFile := writeDurationProbeTools(t)
		const contents = "reader media"
		media := &memoryReadSeekCloser{Reader: bytes.NewReader([]byte(contents))}
		if _, err := media.Seek(3, io.SeekStart); err != nil {
			t.Fatal(err)
		}

		duration, err := DurationForMediaReaderSeconds(context.Background(), ffmpeg, media)
		if err != nil {
			t.Fatal(err)
		}
		if duration != 12.75 {
			t.Fatalf("duration = %v", duration)
		}
		if offset, err := media.Seek(0, io.SeekCurrent); err != nil || offset != 3 {
			t.Fatalf("input offset = %d, err %v", offset, err)
		}
		if stdin, err := os.ReadFile(stdinFile); err != nil || string(stdin) != contents {
			t.Fatalf("ffprobe stdin = %q, err %v", stdin, err)
		}
		if got := probeInputURL(t, argsFile); got != "pipe:0" {
			t.Fatalf("ffprobe input = %q", got)
		}
	})

	t.Run("file without fd protocol", func(t *testing.T) {
		ffmpeg, argsFile, _ := writeDurationProbeTools(t)
		ffmpegProtocolCache.Store(ffmpeg+"|fd", false)
		t.Cleanup(func() { ffmpegProtocolCache.Delete(ffmpeg + "|fd") })
		file, err := os.Create(filepath.Join(t.TempDir(), "media.mp4"))
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if _, err := file.WriteString("media"); err != nil {
			t.Fatal(err)
		}

		if _, err := DurationForMediaReaderSeconds(context.Background(), ffmpeg, file); err != nil {
			t.Fatal(err)
		}
		if got := probeInputURL(t, argsFile); got != "pipe:0" {
			t.Fatalf("ffprobe input = %q", got)
		}
	})
}

func TestDurationForMediaReaderSecondsContext(t *testing.T) {
	ffmpeg, _, _ := writeDurationProbeTools(t)
	t.Setenv("GO2TV_PROBE_BLOCK", "1")
	media := &memoryReadSeekCloser{Reader: bytes.NewReader([]byte("media"))}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := DurationForMediaReaderSeconds(ctx, ffmpeg, media)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}
