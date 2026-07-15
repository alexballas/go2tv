package mediaserver

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/playback"
)

type readSeekCloser struct{ *bytes.Reader }

func (readSeekCloser) Close() error { return nil }

func byteOpener(data []byte) playback.SourceOpener {
	return func(context.Context) (io.ReadSeekCloser, time.Time, error) {
		return readSeekCloser{bytes.NewReader(data)}, time.Unix(1, 0), nil
	}
}

func mediaRequest(data []byte, extension, mediaType string) playback.ServerRequest {
	return playback.ServerRequest{Media: byteOpener(data), MediaExt: extension, MediaType: mediaType}
}

func startTestServer(t *testing.T, server *Server, request playback.ServerRequest) playback.MediaRoute {
	t.Helper()
	route, err := server.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Stop(context.Background()) })
	select {
	case <-server.Ready():
	default:
		t.Fatal("ready not closed")
	}
	return route
}

func TestListenerFirstActualAddressAndStartupError(t *testing.T) {
	server := New(Config{ListenAddr: "127.0.0.1:0"})
	route := startTestServer(t, server, mediaRequest([]byte("media"), ".mp4", "video/mp4"))
	u, err := url.Parse(route.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != server.Addr() || strings.HasSuffix(u.Host, ":0") {
		t.Fatalf("route host %q, addr %q", u.Host, server.Addr())
	}
	if strings.Contains(route.URL, "movie") || strings.Contains(route.URL, "private") {
		t.Fatalf("route leaks filename: %q", route.URL)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || len(parts[1]) != tokenBytes*2 || len(strings.TrimSuffix(parts[3], ".mp4")) != tokenBytes*2 {
		t.Fatalf("route token shape %q", u.Path)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	blocked := New(Config{ListenAddr: listener.Addr().String()})
	if _, err := blocked.Start(context.Background(), mediaRequest(nil, "", "")); err == nil {
		t.Fatal("expected bind error")
	}
}

func TestTargetAwareRouteLocalBind(t *testing.T) {
	server := New(Config{})
	request := mediaRequest([]byte("media"), ".mp4", "video/mp4")
	request.Target = playback.Device{ID: "renderer", Endpoint: "http://127.0.0.1:1400/desc.xml"}
	route := startTestServer(t, server, request)
	u, err := url.Parse(route.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("route host = %q err=%v", u.Host, err)
	}
}

func TestTokenRotationRevocationAndStaleRoutes(t *testing.T) {
	server := New(Config{ListenAddr: "127.0.0.1:0"})
	first := startTestServer(t, server, mediaRequest([]byte("media"), ".mp4", "video/mp4"))
	firstURL, _ := url.Parse(first.URL)
	added, err := server.Add(context.Background(), playback.RouteRequest{ID: "caller-name.vtt", MediaType: "text/vtt", Contents: []byte("WEBVTT")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(added.URL, "caller-name") {
		t.Fatalf("caller ID leaked in route: %q", added.URL)
	}
	if err := server.Remove(context.Background(), added.ID); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(added.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("revoked status %d", response.StatusCode)
	}
	if err := server.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, err := server.Start(context.Background(), mediaRequest([]byte("media"), ".mp4", "video/mp4"))
	if err != nil {
		t.Fatal(err)
	}
	secondURL, _ := url.Parse(second.URL)
	if firstURL.Path == secondURL.Path {
		t.Fatal("session route reused")
	}
	stale := *secondURL
	stale.Path = firstURL.Path
	response, err = http.Get(stale.String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("stale status %d", response.StatusCode)
	}
}

func TestSRTSubtitleRegisteredBeforeReady(t *testing.T) {
	server := New(Config{ListenAddr: "127.0.0.1:0"})
	route := startTestServer(t, server, playback.ServerRequest{
		Media: byteOpener([]byte("media")), MediaExt: ".mp4",
		Subtitle: byteOpener([]byte("subtitle")), SubtitleExt: ".srt", MediaType: "video/mp4",
	})
	if route.SubtitleURL == "" || strings.Contains(route.SubtitleURL, "words") {
		t.Fatalf("subtitle route %q", route.SubtitleURL)
	}
	response, err := http.Get(route.SubtitleURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "subtitle" || response.Header.Get("Content-Type") != "text/srt; charset=utf-8" {
		t.Fatalf("subtitle status=%d type=%q body=%q", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
}

func TestGETHEADMethodRangeAndCallbackCoexist(t *testing.T) {
	payload := []byte("0123456789")
	var callbacks atomic.Int32
	server := New(Config{
		ListenAddr: "127.0.0.1:0",
		Callback: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			callbacks.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}),
	})
	route := startTestServer(t, server, mediaRequest(payload, ".mp4", "video/mp4"))

	request, _ := http.NewRequest(http.MethodGet, route.URL, nil)
	request.Header.Set("Range", "bytes=2-5")
	request.Header.Set("getcontentFeatures.dlna.org", "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusPartialContent || string(body) != "2345" {
		t.Fatalf("range status=%d body=%q", response.StatusCode, body)
	}
	if features := response.Header.Get("contentFeatures.dlna.org"); !strings.Contains(features, "DLNA.ORG_OP=01") {
		t.Fatalf("content features = %q", features)
	}
	if mode := response.Header.Get("transferMode.dlna.org"); mode != "Streaming" {
		t.Fatalf("transfer mode = %q", mode)
	}
	request, _ = http.NewRequest(http.MethodHead, route.URL, nil)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength != int64(len(payload)) {
		t.Fatalf("HEAD status=%d length=%d", response.StatusCode, response.ContentLength)
	}
	request, _ = http.NewRequest(http.MethodPost, route.URL, nil)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d", response.StatusCode)
	}
	request, _ = http.NewRequest("NOTIFY", server.CallbackURL(), strings.NewReader("event"))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || callbacks.Load() != 1 {
		t.Fatalf("callback status=%d calls=%d", response.StatusCode, callbacks.Load())
	}
}

func TestStopDuringTranscodeRepeatedCloseAndDone(t *testing.T) {
	entered := make(chan struct{})
	exited := make(chan struct{})
	server := New(Config{
		ListenAddr: "127.0.0.1:0",
		Transcode: func(ctx context.Context, _ http.ResponseWriter, _ io.ReadCloser, _ playback.ServerRequest) error {
			close(entered)
			<-ctx.Done()
			close(exited)
			return ctx.Err()
		},
	})
	request := mediaRequest([]byte("media"), ".mp4", "video/mp4")
	request.Transcode = true
	route := startTestServer(t, server, request)
	requestDone := make(chan struct{})
	go func() {
		response, err := http.Get(route.URL)
		if err == nil {
			response.Body.Close()
		}
		close(requestDone)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("transcode did not start")
	}
	if err := server.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := server.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("transcode did not stop")
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("request goroutine leaked")
	}
	if err, ok := <-server.Done(); !ok || err != nil {
		t.Fatalf("done err=%v ok=%v", err, ok)
	}
}

func TestChromecastTranscodeResponseHeaders(t *testing.T) {
	server := New(Config{
		ListenAddr: "127.0.0.1:0",
		Transcode: func(_ context.Context, w http.ResponseWriter, _ io.ReadCloser, _ playback.ServerRequest) error {
			_, err := w.Write([]byte("transcoded"))
			return err
		},
	})
	request := mediaRequest([]byte("media"), ".mp4", "video/mp4")
	request.Transcode = true
	request.Target.Protocol = "Chromecast"
	route := startTestServer(t, server, request)
	response, err := http.Get(route.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "transcoded" {
		t.Fatalf("response status=%d body=%q", response.StatusCode, body)
	}
	if got := response.Header.Get("Content-Type"); got != "video/mp4" {
		t.Fatalf("content type = %q", got)
	}
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("CORS = %q", got)
	}
}

func TestChromecastSubtitleRouteSupportsCrossOriginFetch(t *testing.T) {
	subtitle := []byte("WEBVTT\n\n00:00.000 --> 00:01.000\nHello\n")
	server := New(Config{ListenAddr: "127.0.0.1:0"})
	request := mediaRequest([]byte("media"), ".mp4", "video/mp4")
	request.Target.Protocol = "Chromecast"
	request.Subtitle = byteOpener(subtitle)
	request.SubtitleExt = ".vtt"
	route := startTestServer(t, server, request)

	httpRequest, err := http.NewRequest(http.MethodGet, route.SubtitleURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.Header.Set("Origin", "https://www.gstatic.com")
	response, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Equal(body, subtitle) {
		t.Fatalf("subtitle response status=%d body=%q", response.StatusCode, body)
	}
	if got := response.Header.Get("Content-Type"); got != "text/vtt; charset=utf-8" {
		t.Fatalf("subtitle content type = %q", got)
	}
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("subtitle CORS = %q", got)
	}
}

func TestArtworkContentAddressedImmutable(t *testing.T) {
	server := New(Config{ListenAddr: "127.0.0.1:0"})
	startTestServer(t, server, mediaRequest(nil, ".mp4", "video/mp4"))
	one, err := server.AddArtwork(context.Background(), "image/jpeg", []byte("cover"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := server.AddArtwork(context.Background(), "image/jpeg", []byte("cover"))
	if err != nil {
		t.Fatal(err)
	}
	if one.URL != two.URL {
		t.Fatalf("same content routes differ: %q %q", one.URL, two.URL)
	}
	response, err := http.Get(one.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if got := response.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("cache control %q", got)
	}
}
