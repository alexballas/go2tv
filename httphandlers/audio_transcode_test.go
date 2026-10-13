package httphandlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

func TestRadioAudioRelaySurvivesReceiverProbes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	upstream := radioTranscodeTestUpstream(t)
	tt := []struct {
		name       string
		chromecast bool
	}{{name: "DLNA"}, {name: "Chromecast", chromecast: true}}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ffmpeg := filepath.Join(dir, "ffmpeg")
			argsFile := filepath.Join(dir, "args")
			if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GO2TV_RADIO_ARGS\"\nprintf 'encoded AAC'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GO2TV_RADIO_ARGS", argsFile)
			server := NewServer("")
			var tv *soapcalls.TVPayload
			var opts *utils.TranscodeOptions
			if tc.chromecast {
				opts = &utils.TranscodeOptions{FFmpegPath: ffmpeg, AudioOnly: true}
			} else {
				tv = &soapcalls.TVPayload{MediaType: "audio/ogg", Transcode: true, FFmpegPath: ffmpeg}
			}
			server.AddHandler("/radio.aac", tv, opts, RemoteMediaURL(upstream+"/live.m3u8"))
			for _, method := range []string{http.MethodHead, http.MethodGet, http.MethodGet} {
				request := httptest.NewRequest(method, "/radio.aac", nil)
				request.Header.Set("getcontentFeatures.dlna.org", "1")
				response := httptest.NewRecorder()
				server.ServeMediaHandler().ServeHTTP(response, request)
				if response.Code != 200 || response.Header().Get("Content-Type") != utils.AudioTranscodeMediaType {
					t.Fatalf("%s response: %d, %v", method, response.Code, response.Header())
				}
				if method == http.MethodHead {
					if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
						t.Fatal("HEAD started FFmpeg")
					}
					continue
				}
				if response.Body.String() != "encoded AAC" {
					t.Fatalf("%s body = %q", method, response.Body.String())
				}
				args, err := os.ReadFile(argsFile)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(args), "http://127.0.0.1:") || !strings.Contains(string(args), "-vn\n") || !strings.Contains(string(args), "adts\n") {
					t.Fatalf("FFmpeg did not receive radio URL/audio output: %s", args)
				}
				features := response.Header()["contentFeatures.dlna.org"]
				if len(features) != 1 || features[0] != "DLNA.ORG_CI=1" {
					t.Fatalf("converted stream features: %v", response.Header())
				}
			}
		})
	}
}

func TestRadioTranscodeStartupFailureReturnsError(t *testing.T) {
	server := NewServer("")
	server.AddHandler("/radio", nil, &utils.TranscodeOptions{FFmpegPath: filepath.Join(t.TempDir(), "missing-ffmpeg"), AudioOnly: true}, RemoteMediaURL(radioTranscodeTestUpstream(t)+"/live"))
	request := httptest.NewRequest(http.MethodGet, "/radio", nil)
	response := httptest.NewRecorder()
	server.ServeMediaHandler().ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("failed transcode returned HTTP %d", response.Code)
	}
}

func TestRadioAudioReconnectDoesNotKillActiveStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	upstream := radioTranscodeTestUpstream(t)
	dir := t.TempDir()
	ffmpeg := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(ffmpeg, []byte("#!/bin/sh\nhead -c 8192 /dev/zero\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tt := []struct {
		name       string
		chromecast bool
	}{{name: "DLNA"}, {name: "Chromecast", chromecast: true}}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			server := NewServer("")
			var tv *soapcalls.TVPayload
			var opts *utils.TranscodeOptions
			if tc.chromecast {
				opts = &utils.TranscodeOptions{FFmpegPath: ffmpeg, AudioOnly: true}
			} else {
				tv = &soapcalls.TVPayload{MediaType: "audio/mpeg", Transcode: true, FFmpegPath: ffmpeg}
			}
			server.AddHandler("/radio", tv, opts, RemoteMediaURL(upstream+"/live"))
			receiver := httptest.NewServer(server.ServeMediaHandler())
			defer receiver.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			open := func() *http.Response {
				t.Helper()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, receiver.URL+"/radio", nil)
				if err != nil {
					t.Fatal(err)
				}
				response, err := receiver.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = response.Body.Close() })
				if _, err := io.CopyN(io.Discard, response.Body, 8192); err != nil {
					t.Fatalf("stream startup: %v", err)
				}
				return response
			}
			first := open()
			ended := make(chan error, 1)
			go func() {
				_, err := io.Copy(io.Discard, first.Body)
				ended <- err
			}()
			open()
			select {
			case err := <-ended:
				t.Fatalf("reconnect ended active audio stream: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			cancel()
			select {
			case <-ended:
			case <-time.After(time.Second):
				t.Fatal("audio stream survived cancellation")
			}
		})
	}
}

func radioTranscodeTestUpstream(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/live.m3u8" {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nsegment.aac\n#EXT-X-ENDLIST\n")
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = io.WriteString(w, "station audio bytes")
	}))
	t.Cleanup(server.Close)
	return server.URL
}
