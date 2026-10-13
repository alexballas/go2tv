package utils

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

const maxRadioPlaylistBytes = 1 << 20

var radioPlaylistURI = regexp.MustCompile(`URI="([^"]+)"`)

// audioStreamProxy lets FFmpeg use Go's TLS support, including on Android
// builds without a TLS backend. Targets use private signed routes; clients
// cannot supply arbitrary upstream addresses in proxy requests.
type audioStreamProxy struct {
	baseURL string
	server  *http.Server
	key     [32]byte
}

func newAudioStreamProxy(ctx context.Context, upstream string) (*audioStreamProxy, string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("radio proxy listen: %w", err)
	}
	proxy := &audioStreamProxy{baseURL: "http://" + listener.Addr().String()}
	if _, err := rand.Read(proxy.key[:]); err != nil {
		_ = listener.Close()
		return nil, "", fmt.Errorf("radio proxy key: %w", err)
	}
	proxy.server = &http.Server{Handler: proxy, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { _ = proxy.server.Serve(listener) }()
	return proxy, proxy.register(upstream), nil
}

func (p *audioStreamProxy) close() { _ = p.server.Close() }

// Probe the rewritten response so extensionless/script playlists and ordinary
// audio URLs select the correct FFmpeg demuxer options. HEAD does not retain
// an upstream reader after preparation.
func (p *audioStreamProxy) inputIsHLS(ctx context.Context, inputURL string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, streamHTTPPreparationTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, inputURL, nil)
	if err != nil {
		return false, fmt.Errorf("radio input probe: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false, fmt.Errorf("radio input probe: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("radio input probe status: %d", response.StatusCode)
	}
	return IsHLSStream("", response.Header.Get("Content-Type")), nil
}

func (p *audioStreamProxy) register(upstream string, fallbackExtension ...string) string {
	extension := ""
	if parsed, err := url.Parse(upstream); err == nil {
		extension = path.Ext(parsed.Path)
	}
	if extension == "" && len(fallbackExtension) > 0 {
		extension = fallbackExtension[0]
	}
	// Signed routes stay reusable without retaining every live HLS segment.
	mac := hmac.New(sha256.New, p.key[:])
	_, _ = mac.Write([]byte(upstream))
	return p.baseURL + "/" + base64.RawURLEncoding.EncodeToString([]byte(upstream)) + "/" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) + url.PathEscape(extension)
}

func (p *audioStreamProxy) upstream(route string) string {
	encoded, signature, found := strings.Cut(strings.TrimPrefix(route, "/"), "/")
	if !found {
		return ""
	}
	signature, _, _ = strings.Cut(signature, ".")
	upstream, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	sum, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, p.key[:])
	_, _ = mac.Write(upstream)
	if !hmac.Equal(sum, mac.Sum(nil)) {
		return ""
	}
	return string(upstream)
}

func (p *audioStreamProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	upstream := p.upstream(r.URL.Path)
	if upstream == "" {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	response, err := streamURLResponseHeaders(ctx, upstream, http.Header{"Range": r.Header.Values("Range"), "User-Agent": {"Go2TV"}})
	if err != nil {
		http.Error(w, "radio upstream unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	mediaType := response.Header.Get("Content-Type")
	base := response.Request.URL
	deadline := time.AfterFunc(streamHTTPPreparationTimeout, cancel)
	defer deadline.Stop()
	reader := bufio.NewReader(response.Body)
	prefix, _ := reader.Peek(10)
	isPlaylist := bytes.HasPrefix(bytes.TrimPrefix(prefix, []byte{0xef, 0xbb, 0xbf}), []byte("#EXTM3U"))
	if IsHLSStream(base.String(), mediaType) || isPlaylist {
		if !isPlaylist {
			http.Error(w, "invalid radio playlist", http.StatusBadGateway)
			return
		}
		body, err := io.ReadAll(io.LimitReader(reader, maxRadioPlaylistBytes+1))
		if err != nil || len(body) > maxRadioPlaylistBytes {
			http.Error(w, "invalid radio playlist", http.StatusBadGateway)
			return
		}
		playlist := p.rewritePlaylist(string(body), base)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, playlist)
		}
		return
	}
	deadline.Stop()
	if ctx.Err() != nil {
		http.Error(w, "radio upstream timeout", http.StatusGatewayTimeout)
		return
	}
	for _, header := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if value := response.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, reader)
	}
}

func (p *audioStreamProxy) rewritePlaylist(playlist string, base *url.URL) string {
	playlist = strings.TrimPrefix(playlist, "\ufeff")
	rewrite := func(value string) string {
		reference, err := url.Parse(strings.TrimSpace(value))
		if err != nil {
			return value
		}
		target := base.ResolveReference(reference)
		if target.Scheme != "http" && target.Scheme != "https" {
			return value
		}
		return p.register(target.String(), ".ts")
	}
	lines := strings.Split(playlist, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
		case strings.HasPrefix(trimmed, "#"):
			lines[i] = radioPlaylistURI.ReplaceAllStringFunc(line, func(attribute string) string {
				return `URI="` + rewrite(attribute[5:len(attribute)-1]) + `"`
			})
		default:
			lines[i] = rewrite(trimmed)
		}
	}
	return strings.Join(lines, "\n")
}
