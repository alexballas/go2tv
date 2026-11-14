package playback

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChromecastExternalURLPolicy(t *testing.T) {
	subsPath := filepath.Join(t.TempDir(), "subs.srt")
	if err := os.WriteFile(subsPath, []byte("1\n00:00:00,000 --> 00:00:01,000\nHi\n"), 0o600); err != nil {
		t.Fatalf("write subs: %v", err)
	}

	tests := []struct {
		name            string
		mediaURL        string
		mediaType       string
		transcode       bool
		subtitlesPath   string
		wantTranscode   bool
		wantDirect      bool
		wantLocalServer bool
	}{
		{
			name:            "mp4 direct",
			mediaURL:        "https://example.com/video.mp4",
			mediaType:       "video/mp4",
			wantDirect:      true,
			wantLocalServer: false,
		},
		{
			name:            "mp4 transcode",
			mediaURL:        "https://example.com/video.mp4",
			mediaType:       "video/mp4",
			transcode:       true,
			wantTranscode:   true,
			wantDirect:      false,
			wantLocalServer: true,
		},
		{
			name:            "hls disables transcode",
			mediaURL:        "https://example.com/live.m3u8",
			mediaType:       "application/vnd.apple.mpegurl",
			transcode:       true,
			wantDirect:      true,
			wantLocalServer: false,
		},
		{
			name:            "direct with subtitles hosts sidecar",
			mediaURL:        "https://example.com/video.mp4",
			mediaType:       "video/mp4",
			subtitlesPath:   subsPath,
			wantDirect:      true,
			wantLocalServer: true,
		},
		{
			name:            "requested audio relays through transcoder",
			mediaURL:        "https://example.com/audio.mp3",
			mediaType:       "audio/mpeg",
			transcode:       true,
			wantTranscode:   true,
			wantLocalServer: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChromecastExternalURLPolicy(tt.mediaURL, tt.mediaType, tt.transcode, tt.subtitlesPath)
			if got.Transcode != tt.wantTranscode {
				t.Fatalf("Transcode = %v, want %v", got.Transcode, tt.wantTranscode)
			}
			if got.DirectMediaURL != tt.wantDirect {
				t.Fatalf("DirectMediaURL = %v, want %v", got.DirectMediaURL, tt.wantDirect)
			}
			if got.NeedsLocalServer != tt.wantLocalServer {
				t.Fatalf("NeedsLocalServer = %v, want %v", got.NeedsLocalServer, tt.wantLocalServer)
			}
		})
	}
}

func TestSkipTarget(t *testing.T) {
	tt := []struct {
		name                     string
		current, delta, duration int
		want                     int
	}{
		{"forward", 100, SkipForwardSeconds, 1000, 130},
		{"back past start", 4, -SkipBackSeconds, 1000, 0},
		{"forward stops short of end", 990, SkipForwardSeconds, 1000, 999},
		{"unknown duration", 100, SkipForwardSeconds, 0, 130},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := SkipTarget(tc.current, tc.delta, tc.duration); got != tc.want {
				t.Fatalf("SkipTarget(%d, %d, %d) = %d, want %d", tc.current, tc.delta, tc.duration, got, tc.want)
			}
		})
	}
}
