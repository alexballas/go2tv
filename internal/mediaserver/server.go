package mediaserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/utils"
)

const tokenBytes = 32

var (
	ErrAlreadyStarted = errors.New("media server already started")
	ErrNotStarted     = errors.New("media server not started")
	ErrOpenRequired   = errors.New("media opener required")
	ErrTargetRequired = errors.New("renderer target required")
)

// TranscodeFunc streams transcoded output. It must stop when ctx is canceled.
type TranscodeFunc func(context.Context, http.ResponseWriter, io.ReadCloser, playback.ServerRequest) error

type Config struct {
	ListenAddr string
	Transcode  TranscodeFunc
	Callback   http.Handler
	Rand       io.Reader
}

type activeRequest struct {
	cancel context.CancelFunc
	closer io.Closer
}

type route struct {
	id        string
	path      string
	mediaType string
	open      playback.SourceOpener
	extension string
	contents  []byte
	request   playback.ServerRequest
	artwork   bool
}

// Server owns one renderer-facing listener and one playback session at a time.
type Server struct {
	cfg Config

	mu       sync.Mutex
	listener net.Listener
	http     *http.Server
	routes   map[string]route
	byID     map[string]string
	active   map[uint64]activeRequest
	next     uint64
	ready    chan struct{}
	done     chan error
	session  string
	wg       sync.WaitGroup
}

func New(cfg Config) *Server {
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	return &Server{cfg: cfg, routes: make(map[string]route), byID: make(map[string]string), active: make(map[uint64]activeRequest)}
}

func (s *Server) Ready() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *Server) Done() <-chan error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) Start(ctx context.Context, request playback.ServerRequest) (playback.MediaRoute, error) {
	if request.Media == nil {
		return playback.MediaRoute{}, ErrOpenRequired
	}
	listenAddr := s.cfg.ListenAddr
	if listenAddr == "" {
		var err error
		listenAddr, err = targetListenAddr(ctx, request.Target.Endpoint)
		if err != nil {
			return playback.MediaRoute{}, err
		}
	}

	s.mu.Lock()
	if s.listener != nil {
		s.mu.Unlock()
		return playback.MediaRoute{}, ErrAlreadyStarted
	}
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		s.mu.Unlock()
		return playback.MediaRoute{}, fmt.Errorf("listen media server: %w", err)
	}
	session, err := randomToken(s.cfg.Rand)
	if err != nil {
		_ = listener.Close()
		s.mu.Unlock()
		return playback.MediaRoute{}, fmt.Errorf("media session token: %w", err)
	}
	s.listener = listener
	s.session = session
	s.ready = make(chan struct{})
	s.done = make(chan error, 1)
	s.routes = make(map[string]route)
	s.byID = make(map[string]string)
	s.active = make(map[uint64]activeRequest)
	mediaRoute, err := s.newSourceRouteLocked("media", request.Media, request.MediaExt, request.MediaType, request)
	if err != nil {
		s.resetLocked()
		_ = listener.Close()
		s.mu.Unlock()
		return playback.MediaRoute{}, err
	}
	var subtitleRoute route
	if request.Subtitle != nil && !request.BurnSubtitle {
		subtitleRequest := playback.ServerRequest{Media: request.Subtitle, MediaExt: request.SubtitleExt, MediaType: subtitleMediaType(request.SubtitleExt), Target: request.Target}
		subtitleRoute, err = s.newSourceRouteLocked("subtitle", request.Subtitle, request.SubtitleExt, subtitleRequest.MediaType, subtitleRequest)
		if err != nil {
			s.resetLocked()
			_ = listener.Close()
			s.mu.Unlock()
			return playback.MediaRoute{}, err
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.serveHTTP)
	if s.cfg.Callback != nil {
		callbackToken, tokenErr := randomToken(s.cfg.Rand)
		if tokenErr != nil {
			s.resetLocked()
			_ = listener.Close()
			s.mu.Unlock()
			return playback.MediaRoute{}, fmt.Errorf("callback token: %w", tokenErr)
		}
		callbackPath := "/renderer/" + s.session + "/callback/" + callbackToken
		s.routes[callbackPath] = route{path: callbackPath}
		mux.Handle(callbackPath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "NOTIFY" {
				w.Header().Set("Allow", "NOTIFY")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.cfg.Callback.ServeHTTP(w, r)
		}))
	}
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       30 * time.Second,
		// Streaming responses deliberately have no WriteTimeout.
	}
	s.http = httpServer
	ready := s.ready
	done := s.done
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		close(ready)
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			err = nil
		}
		done <- err
		close(done)
	}()
	base := "http://" + listener.Addr().String()
	s.mu.Unlock()

	select {
	case <-ready:
		result := playback.MediaRoute{URL: base + mediaRoute.path, ID: mediaRoute.id}
		if subtitleRoute.path != "" {
			result.SubtitleURL = base + subtitleRoute.path
		}
		return result, nil
	case <-ctx.Done():
		_ = s.Stop(context.Background())
		return playback.MediaRoute{}, ctx.Err()
	}
}

func subtitleMediaType(extension string) string {
	if strings.EqualFold(extension, ".srt") {
		return "text/srt; charset=utf-8"
	}
	return "text/vtt; charset=utf-8"
}

func (s *Server) CallbackURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	for path := range s.routes {
		if strings.Contains(path, "/callback/") {
			return "http://" + s.listener.Addr().String() + path
		}
	}
	return ""
}

func (s *Server) Add(_ context.Context, request playback.RouteRequest) (playback.MediaRoute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return playback.MediaRoute{}, ErrNotStarted
	}
	purpose := "subtitle"
	if strings.HasPrefix(request.MediaType, "image/") {
		purpose = "artwork"
	}
	var r route
	var err error
	if request.Open != nil {
		r, err = s.newSourceRouteLocked(purpose, request.Open, request.Extension, request.MediaType, playback.ServerRequest{Media: request.Open, MediaExt: request.Extension, MediaType: request.MediaType})
	} else {
		r, err = s.newBytesRouteLocked(purpose, request.MediaType, request.Contents)
	}
	if err != nil {
		return playback.MediaRoute{}, err
	}
	return playback.MediaRoute{URL: "http://" + s.listener.Addr().String() + r.path, ID: r.id}, nil
}

// AddMedia registers another primary media route without restarting the
// listener. The caller removes superseded routes after the renderer accepts
// the replacement load.
func (s *Server) AddMedia(_ context.Context, request playback.ServerRequest) (playback.MediaRoute, error) {
	if request.Media == nil {
		return playback.MediaRoute{}, ErrOpenRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return playback.MediaRoute{}, ErrNotStarted
	}
	r, err := s.newSourceRouteLocked("media", request.Media, request.MediaExt, request.MediaType, request)
	if err != nil {
		return playback.MediaRoute{}, err
	}
	return playback.MediaRoute{URL: "http://" + s.listener.Addr().String() + r.path, ID: r.id}, nil
}

// AddArtwork registers content-addressed immutable renderer artwork.
func (s *Server) AddArtwork(ctx context.Context, mediaType string, contents []byte) (playback.MediaRoute, error) {
	return s.Add(ctx, playback.RouteRequest{MediaType: mediaType, Contents: contents})
}

func (s *Server) Remove(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, ok := s.byID[id]
	if !ok {
		return nil
	}
	delete(s.byID, id)
	delete(s.routes, path)
	return nil
}

func (s *Server) Stop(_ context.Context) error {
	s.mu.Lock()
	if s.listener == nil {
		s.mu.Unlock()
		return nil
	}
	listener := s.listener
	httpServer := s.http
	for _, active := range s.active {
		active.cancel()
		if active.closer != nil {
			_ = active.closer.Close()
		}
	}
	s.routes = make(map[string]route)
	s.byID = make(map[string]string)
	s.listener = nil
	s.http = nil
	s.session = ""
	s.mu.Unlock()

	err := listener.Close()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("close media listener: %w", err)
	}
	if httpServer != nil {
		_ = httpServer.Close()
	}
	s.wg.Wait()
	s.mu.Lock()
	s.active = make(map[uint64]activeRequest)
	s.mu.Unlock()
	return nil
}

func (s *Server) newSourceRouteLocked(purpose string, open playback.SourceOpener, extension, mediaType string, request playback.ServerRequest) (route, error) {
	if open == nil {
		return route{}, ErrOpenRequired
	}
	token, err := randomToken(s.cfg.Rand)
	if err != nil {
		return route{}, fmt.Errorf("%s token: %w", purpose, err)
	}
	id, err := randomToken(s.cfg.Rand)
	if err != nil {
		return route{}, fmt.Errorf("route id: %w", err)
	}
	ext := safeExtension(extension)
	r := route{id: id, path: "/renderer/" + s.session + "/" + purpose + "/" + token + ext, open: open, extension: ext, mediaType: mediaType, request: request}
	s.routes[r.path] = r
	s.byID[id] = r.path
	return r, nil
}

func (s *Server) newBytesRouteLocked(purpose, mediaType string, contents []byte) (route, error) {
	token, err := randomToken(s.cfg.Rand)
	if err != nil {
		return route{}, fmt.Errorf("%s token: %w", purpose, err)
	}
	id, err := randomToken(s.cfg.Rand)
	if err != nil {
		return route{}, fmt.Errorf("route id: %w", err)
	}
	ext := extensionForType(mediaType)
	path := "/renderer/" + s.session + "/" + purpose + "/" + token + ext
	r := route{id: id, path: path, mediaType: mediaType, contents: append([]byte(nil), contents...), artwork: purpose == "artwork"}
	if r.artwork {
		hash := sha256.Sum256(contents)
		path = "/renderer/" + s.session + "/artwork/" + hex.EncodeToString(hash[:]) + ext
		r.path = path
		if existing, ok := s.routes[path]; ok {
			return existing, nil
		}
	}
	s.routes[path] = r
	s.byID[id] = path
	return r, nil
}

func (s *Server) serveHTTP(w http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	r, ok := s.routes[request.URL.Path]
	if !ok {
		s.mu.Unlock()
		http.NotFound(w, request)
		return
	}
	ctx, cancel := context.WithCancel(request.Context())
	s.next++
	requestID := s.next
	s.active[requestID] = activeRequest{cancel: cancel}
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.active, requestID)
		s.mu.Unlock()
	}()

	switch request.Method {
	case http.MethodGet, http.MethodHead:
	default:
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.mediaType != "" {
		w.Header().Set("Content-Type", r.mediaType)
		w.Header().Set("transferMode.dlna.org", "Streaming")
		w.Header().Set("realTimeInfo.dlna.org", "DLNA.ORG_TLAG=*")
	}
	if r.request.Target.Protocol == "Chromecast" {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	if request.Header.Get("getcontentFeatures.dlna.org") == "1" {
		seek := "00"
		if !r.request.Transcode && r.open != nil {
			seek = "01"
		}
		contentFeatures, err := utils.BuildContentFeatures(r.mediaType, seek, r.request.Transcode)
		if err != nil {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("contentFeatures.dlna.org", contentFeatures)
	}
	if r.artwork {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("ETag", `"`+strings.TrimSuffix(filepath.Base(r.path), filepath.Ext(r.path))+`"`)
		if request.Header.Get("If-None-Match") == w.Header().Get("ETag") {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if r.contents != nil {
		http.ServeContent(w, request.WithContext(ctx), "content", time.Time{}, bytes.NewReader(r.contents))
		return
	}
	reader, modTime, err := r.open(ctx)
	if err != nil {
		http.NotFound(w, request)
		return
	}
	defer reader.Close()
	s.mu.Lock()
	if active, exists := s.active[requestID]; exists {
		active.closer = reader
		s.active[requestID] = active
	}
	s.mu.Unlock()
	if r.request.Transcode {
		if s.cfg.Transcode == nil {
			http.Error(w, "transcoding unavailable", http.StatusServiceUnavailable)
			return
		}
		if request.Method == http.MethodHead {
			return
		}
		if err := s.cfg.Transcode(ctx, w, reader, r.request); err != nil && ctx.Err() == nil {
			http.Error(w, "transcode failed", http.StatusInternalServerError)
		}
		return
	}
	http.ServeContent(w, request.WithContext(ctx), "media", modTime, reader)
}

func (s *Server) resetLocked() {
	s.listener = nil
	s.http = nil
	s.session = ""
	s.routes = make(map[string]route)
	s.byID = make(map[string]string)
}

func randomToken(source io.Reader) (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := io.ReadFull(source, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func safeExtension(extension string) string {
	ext := strings.ToLower(extension)
	if len(ext) < 2 || len(ext) > 11 {
		return ""
	}
	for _, r := range ext[1:] {
		if r > unicode.MaxASCII || !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return ""
		}
	}
	return ext
}

func targetListenAddr(ctx context.Context, endpoint string) (string, error) {
	if endpoint == "" {
		return "", ErrTargetRequired
	}
	hostport := endpoint
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Host != "" {
		hostport = parsed.Host
	}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
		port = "80"
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", net.JoinHostPort(host, port))
	if err != nil {
		return "", fmt.Errorf("renderer route address: %w", err)
	}
	local := conn.LocalAddr().(*net.UDPAddr).IP.String()
	_ = conn.Close()
	return net.JoinHostPort(local, "0"), nil
}

func extensionForType(mediaType string) string {
	mediaType = strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	switch mediaType {
	case "text/vtt":
		return ".vtt"
	case "application/x-subrip":
		return ".srt"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	default:
		return ""
	}
}
