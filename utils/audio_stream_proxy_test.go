package utils

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRadioProxyRewritesHTTPSHLSReferences(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/radio/master", http.StatusFound)
		case "/radio/master":
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, "\ufeff#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=128000\nsub/list.m3u8?token=one\n")
		case "/radio/sub/list.m3u8":
			if r.URL.Query().Get("token") != "one" {
				t.Errorf("playlist query lost: %s", r.URL)
			}
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			io.WriteString(w, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"../key\"\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2,\nsegment.aac\n")
		case "/radio/key":
			io.WriteString(w, "0123456789abcdef")
		case "/radio/sub/init.mp4":
			io.WriteString(w, "init")
		case "/radio/sub/segment.aac":
			if r.Header.Get("Range") != "bytes=1-3" {
				t.Errorf("segment range lost: %v", r.Header)
			}
			w.Header().Set("Content-Range", "bytes 1-3/5")
			w.WriteHeader(http.StatusPartialContent)
			io.WriteString(w, "aac")
		default:
			t.Errorf("unexpected upstream URL: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	previous := streamHTTPClient
	streamHTTPClient = upstream.Client()
	defer func() { streamHTTPClient = previous }()
	proxy, entryURL, err := newAudioStreamProxy(context.Background(), upstream.URL+"/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.close()
	get := func(endpoint, byteRange string) string {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		if byteRange != "" {
			req.Header.Set("Range", byteRange)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 && resp.StatusCode != 206 {
			t.Fatalf("proxy status = %d, %s", resp.StatusCode, body)
		}
		return string(body)
	}
	master := get(entryURL, "")
	if !strings.HasPrefix(master, "#EXTM3U\n") {
		t.Fatalf("playlist header corrupted: %s", master)
	}
	playlistURL := ""
	for line := range strings.SplitSeq(master, "\n") {
		if strings.HasPrefix(line, "http://127.0.0.1:") {
			playlistURL = line
		}
	}
	if playlistURL == "" || strings.Contains(master, upstream.URL) {
		t.Fatalf("master references not proxied: %s", master)
	}
	playlist := get(playlistURL, "")
	for _, match := range radioPlaylistURI.FindAllStringSubmatch(playlist, -1) {
		get(match[1], "")
	}
	for line := range strings.SplitSeq(playlist, "\n") {
		if strings.HasPrefix(line, "http://127.0.0.1:") {
			if body := get(line, "bytes=1-3"); body != "aac" {
				t.Fatalf("segment = %q", body)
			}
		}
	}
}

func TestRadioProxyClosesOrdinaryStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, "radio bytes")
	}))
	defer upstream.Close()
	proxy, endpoint, err := newAudioStreamProxy(context.Background(), upstream.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(endpoint)
	if err != nil {
		proxy.close()
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "radio bytes" || response.Header.Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("radio relay: %q, %v", body, err)
	}
	proxy.close()
	if response, err := http.Get(endpoint); err == nil {
		response.Body.Close()
		t.Fatal("proxy still running after close")
	}
}

func TestRadioProxyRelaysICYTitlesOnPlaybackConnection(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "audio/mpeg")
		if r.Header.Get("Icy-MetaData") != "1" {
			io.WriteString(w, "radio")
			return
		}
		w.Header().Set("icy-metaint", "3")
		metadata := "StreamTitle='Song';" + strings.Repeat("\x00", 13)
		w.Header().Set("Content-Length", "38")
		io.WriteString(w, "rad\x02"+metadata+"io")
	}))
	defer upstream.Close()
	var titles []string
	proxy, endpoint, err := newAudioStreamProxy(context.Background(), upstream.URL, func(title string) { titles = append(titles, title) })
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.close()
	response, err := http.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	// The upstream length counts metadata the relay removed.
	if err != nil || string(body) != "radio" || response.ContentLength == 38 {
		t.Fatalf("ICY relay = %q (length %d), %v", body, response.ContentLength, err)
	}
	if len(titles) != 1 || titles[0] != "Song" || requests.Load() != 1 {
		t.Fatalf("titles = %q after %d upstream requests", titles, requests.Load())
	}
}

func TestRadioProxyRoutesRejectForgedTargetsAndSurviveLiveSegments(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = io.WriteString(w, "radio bytes")
	}))
	defer upstream.Close()
	proxy, endpoint, err := newAudioStreamProxy(context.Background(), upstream.URL+"/live", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.close()
	other, _, err := newAudioStreamProxy(context.Background(), upstream.URL+"/live", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.close()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	_, signature, _ := strings.Cut(strings.TrimPrefix(parsed.Path, "/"), "/")
	forged := proxy.baseURL + "/" + base64.RawURLEncoding.EncodeToString([]byte(upstream.URL+"/forged")) + "/" + signature
	tt := []struct {
		name, endpoint string
	}{
		{name: "changed target", endpoint: forged},
		{name: "another session", endpoint: other.baseURL + parsed.Path},
		{name: "unregistered route", endpoint: proxy.baseURL + "/unknown"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			response, err := http.Get(tc.endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusNotFound || requests.Load() != 0 {
				t.Fatalf("forged target reached upstream: HTTP %d, %d requests", response.StatusCode, requests.Load())
			}
		})
	}
	// A live station introduces new segment URLs continually; reusable playlist
	// routes must stay valid throughout playback.
	for i := range 10_000 {
		proxy.register(upstream.URL + "/" + strconv.Itoa(i) + ".aac")
	}
	response, err := http.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "radio bytes" {
		t.Fatalf("original route stopped working: HTTP %d, %q, %v", response.StatusCode, body, err)
	}
}

func TestRadioTranscodeHTTPSHLSProducesAAC(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	segment, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-c:a", "aac", "-f", "mpegts", "pipe:1").Output()
	if err != nil {
		t.Fatalf("generate HLS segment: %v", err)
	}
	key := []byte("0123456789abcdef")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(segment)%aes.BlockSize
	encrypted := append(bytes.Clone(segment), bytes.Repeat([]byte{byte(padding)}, padding)...)
	cipher.NewCBCEncrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(encrypted, encrypted)
	aac, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-c:a", "aac", "-f", "adts", "pipe:1").Output()
	if err != nil {
		t.Fatalf("generate AAC segment: %v", err)
	}
	mp3, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-c:a", "mp3", "-f", "mp3", "pipe:1").Output()
	if err != nil {
		t.Fatalf("generate MP3 segment: %v", err)
	}
	mp4, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-c:a", "aac", "-movflags", "frag_keyframe+empty_moov", "-f", "mp4", "pipe:1").Output()
	if err != nil {
		t.Fatalf("generate fragmented MP4: %v", err)
	}
	// Split the generated initialization section from its first fragment.
	fragmentStart := bytes.Index(mp4, []byte("moof")) - 4
	if fragmentStart <= 0 {
		t.Fatal("generated MP4 lacks a fragment")
	}
	tt := []struct {
		name, segmentPath, mediaType string
		data, init                   []byte
		plain                        bool
		keyURI                       string
	}{
		{name: "extensionless transport stream", segmentPath: "/segment", mediaType: "video/mp2t", data: segment},
		{name: "script transport stream", segmentPath: "/segment.php?token=one", mediaType: "video/mp2t", data: segment},
		{name: "script packed AAC", segmentPath: "/segment.php?token=one", mediaType: "audio/aac", data: aac},
		{name: "AAC suffix", segmentPath: "/segment.aac", mediaType: "audio/aac", data: aac},
		{name: "script packed MP3", segmentPath: "/segment.php?token=one", mediaType: "audio/mpeg", data: mp3},
		{name: "script fragmented MP4", segmentPath: "/segment.php?token=one", mediaType: "audio/mp4", data: mp4[fragmentStart:], init: mp4[:fragmentStart]},
		{name: "plain AAC control", segmentPath: "/radio", mediaType: "audio/aac", data: aac, plain: true},
		{name: "plain MP3 control", segmentPath: "/radio", mediaType: "audio/mpeg", data: mp3, plain: true},
		{name: "AES with HTTP key", segmentPath: "/segment.php?token=one", mediaType: "video/mp2t", data: encrypted, keyURI: "/key.php?token=one"},
		{name: "AES with inline key", segmentPath: "/segment.php?token=one", mediaType: "video/mp2t", data: encrypted, keyURI: "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(key)},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/radio" && !tc.plain {
					w.Header().Set("Content-Type", "application/octet-stream")
					_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n")
					if tc.keyURI != "" {
						_, _ = fmt.Fprintf(w, "#EXT-X-KEY:METHOD=AES-128,URI=\"%s\"\n", tc.keyURI)
					}
					if tc.init != nil {
						_, _ = io.WriteString(w, "#EXT-X-MAP:URI=\"/init.php?token=one\"\n")
					}
					_, _ = fmt.Fprintf(w, "#EXTINF:0.2,\n%s\n#EXT-X-ENDLIST\n", tc.segmentPath)
					return
				}
				if r.URL.RequestURI() == "/key.php?token=one" && tc.keyURI != "" {
					w.Header().Set("Content-Type", "application/octet-stream")
					_, _ = w.Write(key)
					return
				}
				if tc.init != nil && r.URL.RequestURI() == "/init.php?token=one" {
					w.Header().Set("Content-Type", "audio/mp4")
					_, _ = w.Write(tc.init)
					return
				}
				if r.URL.RequestURI() != tc.segmentPath {
					t.Errorf("segment URL changed: %s, want %s", r.URL.RequestURI(), tc.segmentPath)
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", tc.mediaType)
				_, _ = w.Write(tc.data)
			}))
			defer upstream.Close()
			previous := streamHTTPClient
			streamHTTPClient = upstream.Client()
			defer func() { streamHTTPClient = previous }()
			var output bytes.Buffer
			var command exec.Cmd
			if err := ServeAudioTranscodedStream(ctx, &output, upstream.URL+"/radio", &command, &TranscodeOptions{FFmpegPath: ffmpeg, AudioOnly: true}); err != nil {
				t.Fatalf("HLS radio transcode: %v", err)
			}
			if data := output.Bytes(); len(data) < 7 || data[0] != 0xff || data[1]&0xf6 != 0xf0 {
				t.Fatalf("HLS output is not AAC: %x", data[:min(len(data), 7)])
			}
		})
	}
}
