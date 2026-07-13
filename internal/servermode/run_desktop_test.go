//go:build !(android || ios)

package servermode

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/playback"
)

func TestPrepareServerRequestChromecastTranscode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		protocol      string
		transcode     bool
		wantExtension string
		wantMediaType string
		wantCast      bool
	}{
		{name: "Chromecast transcode", protocol: "Chromecast", transcode: true, wantExtension: ".mp4", wantMediaType: "video/mp4", wantCast: true},
		{name: "Chromecast direct", protocol: "Chromecast", wantExtension: ".mkv", wantMediaType: "video/x-matroska", wantCast: true},
		{name: "DLNA transcode", protocol: "DLNA", transcode: true, wantExtension: ".mkv", wantMediaType: "video/x-matroska"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := prepareServerRequest(playback.ServerRequest{
				MediaExt: ".mkv", MediaType: "video/x-matroska", Transcode: tt.transcode,
				Target: playback.Device{Protocol: tt.protocol},
			})
			if request.MediaExt != tt.wantExtension || request.MediaType != tt.wantMediaType {
				t.Fatalf("request format = %q %q, want %q %q", request.MediaExt, request.MediaType, tt.wantExtension, tt.wantMediaType)
			}
			if got := isChromecastRequest(request); got != tt.wantCast {
				t.Fatalf("Chromecast request = %v, want %v", got, tt.wantCast)
			}
		})
	}
}

func TestLogStartupNonLoopback(t *testing.T) {
	t.Parallel()
	rootA := filepath.Join(string(filepath.Separator), "media", "films")
	rootB := filepath.Join(string(filepath.Separator), "media", "music")
	cfg := Config{
		Listen:         "0.0.0.0:9666",
		MediaRoots:     []string{rootA, rootB},
		AllowedOrigins: []string{"http://192.0.2.10:9666", "http://media.test:9666"},
	}
	var output bytes.Buffer
	logStartup(&output, cfg, "0.0.0.0:9666")

	want := []string{
		"Web server listening: 0.0.0.0:9666\n",
		"Usable allowed URLs:\n",
		"  http://192.0.2.10:9666/\n",
		"  http://media.test:9666/\n",
		"WARNING: trusted-LAN mode; no TLS. Do not expose to untrusted networks.\n",
		"Media roots:\n",
		"  " + rootA + "\n",
		"  " + rootB + "\n",
		"WARNING: media root paths may enter console/journal logs.\n",
	}
	if got := output.String(); got != strings.Join(want, "") {
		t.Fatalf("output = %q, want %q", got, strings.Join(want, ""))
	}
}

func TestRunNeverReadsPipedStdin(t *testing.T) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()
	original := os.Stdin
	os.Stdin = readEnd
	defer func() { os.Stdin = original }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := make(chan string, 8)
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, Config{Listen: "127.0.0.1:0", MediaRoots: []string{t.TempDir()}}, channelWriter(output))
	}()
	select {
	case line := <-output:
		if !strings.Contains(line, "Web server listening:") {
			t.Fatalf("first output = %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server blocked on piped stdin")
	}
	cancel()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

type channelWriter chan<- string

func (w channelWriter) Write(p []byte) (int, error) {
	w <- string(p)
	return len(p), nil
}
