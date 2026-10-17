package utils

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAudioTranscodeProducesPlayableAAC(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	input, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-f", "wav", "pipe:1").Output()
	if err != nil {
		t.Fatalf("generate audio: %v", err)
	}
	var output bytes.Buffer
	var command exec.Cmd
	if err := ServeAudioTranscodedStream(ctx, &output, bytes.NewReader(input), &command, &TranscodeOptions{FFmpegPath: ffmpeg, AudioOnly: true}); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	if len(data) < 7 || data[0] != 0xff || data[1]&0xf6 != 0xf0 {
		t.Fatalf("output lacks AAC ADTS header: %x", data[:min(7, len(data))])
	}
	decode := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "aac", "-i", "pipe:0", "-f", "null", "-")
	decode.Stdin = bytes.NewReader(data)
	if logs, err := decode.CombinedOutput(); err != nil {
		t.Fatalf("decode AAC: %v: %s", err, logs)
	}
}

func TestAudioTranscodeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var command exec.Cmd
	err := ServeAudioTranscodedStream(ctx, &bytes.Buffer{}, "https://station.test/live.m3u8", &command, &TranscodeOptions{FFmpegPath: "ffmpeg"})
	if !errors.Is(err, context.Canceled) || command.Process != nil {
		t.Fatalf("canceled transcode started: %v, %v", command.Process, err)
	}
}

func TestAudioHLSURLTranscodeWithLegacyFFmpeg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacy FFmpeg fixture requires a shell")
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	input, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-c:a", "aac", "-f", "mpegts", "pipe:1").Output()
	if err != nil {
		t.Fatalf("generate audio: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/radio" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:0.2,\nsegment.ts\n#EXT-X-ENDLIST\n"))
			return
		}
		w.Header().Set("Content-Type", "video/mp2t")
		_, _ = w.Write(input)
	}))
	defer upstream.Close()
	t.Setenv("GO2TV_TEST_FFMPEG_REAL", ffmpeg)
	legacy := filepath.Join(t.TempDir(), "ffmpeg")
	// Older builds reject options added by newer HLS demuxers.
	script := `#!/bin/sh
if [ "$2" = "-h" ]; then
    printf '%s\n' ' -allowed_extensions <string> HLS extensions'
    exit 0
fi
for option do
    case "$option" in
        -allowed_segment_extensions|-extension_picky) exit 1 ;;
    esac
done
exec "$GO2TV_TEST_FFMPEG_REAL" "$@"
`
	if err := os.WriteFile(legacy, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	var command exec.Cmd
	if err := ServeAudioTranscodedStream(ctx, &output, upstream.URL+"/radio", &command, &TranscodeOptions{FFmpegPath: legacy, AudioOnly: true}); err != nil {
		t.Fatalf("legacy FFmpeg URL playback: %v", err)
	}
	decode := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "aac", "-i", "pipe:0", "-f", "null", "-")
	decode.Stdin = &output
	if logs, err := decode.CombinedOutput(); err != nil {
		t.Fatalf("legacy FFmpeg output is not playable: %v: %s", err, logs)
	}
}
