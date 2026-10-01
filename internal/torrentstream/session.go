// Package torrentstream streams verified torrent pieces on demand.
package torrentstream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"

	"go2tv.app/go2tv/v2/internal/mediasource"
)

const readahead = 8 << 20

// File is a selectable media file. Index refers to the original torrent list.
type File struct {
	Index int
	Name  string
	Size  int64
}

type Options struct {
	// CacheDir is the parent of the private cache, required on mobile.
	CacheDir string
}

type Session struct {
	client     *torrent.Client
	storage    storage.ClientImplCloser
	torrent    *torrent.Torrent
	dir        string
	ctx        context.Context
	cancel     context.CancelFunc
	server     *http.Server
	baseURL    string
	mu         sync.Mutex
	selected   *torrent.File
	unregister []func()
	closeOnce  sync.Once
	closeErr   error
}

// Open accepts a magnet URI or a .torrent path. No media is downloaded until
// selected. The private cache is removed when the session closes.
func Open(ctx context.Context, input string) (*Session, error) {
	return OpenWithOptions(ctx, input, Options{})
}

func OpenWithOptions(ctx context.Context, input string, options Options) (*Session, error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(strings.ToLower(input), "magnet:") {
		return open(ctx, input, nil, options)
	}
	mi, err := metainfo.LoadFromFile(input)
	if err != nil {
		return nil, fmt.Errorf("open torrent: %w", err)
	}
	return open(ctx, "", mi, options)
}

// OpenReader also supports mobile content:// torrent documents.
func OpenReader(ctx context.Context, reader io.Reader) (*Session, error) {
	return OpenReaderWithOptions(ctx, reader, Options{})
}

func OpenReaderWithOptions(ctx context.Context, reader io.Reader, options Options) (*Session, error) {
	mi, err := metainfo.Load(reader)
	if err != nil {
		return nil, fmt.Errorf("read torrent: %w", err)
	}
	return open(ctx, "", mi, options)
}

func open(ctx context.Context, magnet string, mi *metainfo.MetaInfo, options Options) (*Session, error) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = options.CacheDir
	return openWithConfig(ctx, magnet, mi, cfg)
}

func openWithConfig(ctx context.Context, magnet string, mi *metainfo.MetaInfo, cfg *torrent.ClientConfig) (_ *Session, err error) {
	if ctx == nil {
		return nil, errors.New("torrent context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var spec *torrent.TorrentSpec
	if mi != nil {
		spec, err = torrent.TorrentSpecFromMetaInfoErr(mi)
	} else {
		spec, err = torrent.TorrentSpecFromMagnetUri(magnet)
	}
	if err != nil {
		return nil, fmt.Errorf("parse torrent: %w", err)
	}
	if spec.InfoHash == (metainfo.Hash{}) && !spec.InfoHashV2.Ok {
		return nil, errors.New("torrent has no valid info hash")
	}
	dir, err := os.MkdirTemp(cfg.DataDir, "go2tv-torrent-*")
	if err != nil {
		return nil, fmt.Errorf("torrent cache: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Session{dir: dir, ctx: ctx, cancel: cancel}
	defer func() {
		if err != nil {
			_ = s.Close()
		}
	}()
	cfg.DataDir = dir
	cfg.ListenPort = 0
	cfg.Seed = false
	cfg.DisableWebtorrent = true
	cfg.Slogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	s.storage = storage.NewFileOpts(storage.NewFileClientOpts{ClientBaseDir: dir, Logger: cfg.Slogger})
	cfg.DefaultStorage = s.storage
	s.client, err = torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("torrent client: %w", err)
	}
	s.torrent, _, err = s.client.AddTorrentSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("add torrent: %w", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("torrent HTTP: %w", err)
	}
	s.baseURL = "http://" + listener.Addr().String()
	s.server = &http.Server{Handler: http.HandlerFunc(s.serve), BaseContext: func(net.Listener) context.Context { return s.ctx }}
	go func() { _ = s.server.Serve(listener) }()
	go func() { <-ctx.Done(); _ = s.Close() }()
	return s, nil
}

// Files waits for magnet metadata, with cancellation, then lists media only.
func (s *Session) Files(ctx context.Context) ([]File, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case <-s.torrent.GotInfo():
	}
	var files []File
	for index, file := range s.torrent.Files() {
		if file.Length() > 0 && MediaMIME(file.DisplayPath()) != "" {
			files = append(files, File{Index: index, Name: file.DisplayPath(), Size: file.Length()})
		}
	}
	if len(files) == 0 {
		return nil, errors.New("torrent contains no supported media files")
	}
	return files, nil
}

// Select downloads this file in the background. Reader windows have higher
// priority, so seeks fetch the requested region ahead of sequential download.
// The returned path is logical; callers must use mediasource, never os.Open.
func (s *Session) Select(index int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return "", err
	}
	select {
	case <-s.torrent.GotInfo():
	default:
		return "", errors.New("torrent metadata unavailable")
	}
	files := s.torrent.Files()
	if index < 0 || index >= len(files) {
		return "", errors.New("invalid torrent file index")
	}
	file := files[index]
	if MediaMIME(file.DisplayPath()) == "" || file.Length() == 0 {
		return "", errors.New("unsupported torrent media")
	}
	if s.selected != nil {
		s.selected.SetPriority(torrent.PiecePriorityNone)
	}
	s.selected = file
	file.Download()
	name := filepath.Base(filepath.FromSlash(file.DisplayPath()))
	path := filepath.Join(s.dir, "media", strconv.Itoa(index), name)
	source := &fileSource{session: s, file: file, url: s.baseURL + "/" + strconv.Itoa(index) + "/" + url.PathEscape(name)}
	s.unregister = append(s.unregister, mediasource.Register(path, source))
	return path, nil
}

func (s *Session) Progress() (completed, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selected != nil {
		return s.selected.BytesCompleted(), s.selected.Length()
	}
	return 0, 0
}

func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		if s.server != nil {
			_ = s.server.Close()
		}
		s.mu.Lock()
		for _, unregister := range s.unregister {
			unregister()
		}
		s.mu.Unlock()
		if s.client != nil {
			s.closeErr = errors.Join(s.client.Close()...)
		}
		if s.storage != nil {
			s.closeErr = errors.Join(s.closeErr, s.storage.Close())
		}
		s.closeErr = errors.Join(s.closeErr, os.RemoveAll(s.dir))
	})
	return s.closeErr
}

func (s *Session) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	index, err := strconv.Atoi(parts[0])
	if err != nil || len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	file := s.selected
	files := s.torrent.Files()
	allowed := index >= 0 && index < len(files) && files[index] == file
	s.mu.Unlock()
	if !allowed || parts[1] != filepath.Base(filepath.FromSlash(file.DisplayPath())) {
		http.NotFound(w, r)
		return
	}
	mediasource.Serve(w, r, parts[1], &fileSource{session: s, file: file})
}

type fileSource struct {
	session *Session
	file    *torrent.File
	url     string
}

func (f *fileSource) URL() string  { return f.url }
func (f *fileSource) MIME() string { return MediaMIME(f.file.DisplayPath()) }
func (f *fileSource) Size() int64  { return f.file.Length() }
func (f *fileSource) Open(ctx context.Context) (io.ReadSeekCloser, error) {
	if err := f.session.ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(f.session.ctx, cancel)
	reader := f.file.NewReader()
	reader.SetContext(ctx)
	reader.SetReadahead(readahead)
	return &sourceReader{Reader: reader, cancel: cancel, stop: stop, url: f.url}, nil
}

type sourceReader struct {
	torrent.Reader
	cancel context.CancelFunc
	stop   func() bool
	url    string
	mu     sync.Mutex
	closed bool
	err    error
}

// URL lets reader-based playback pipelines preserve FFmpeg input seeking.
func (r *sourceReader) URL() string { return r.url }

func (r *sourceReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	return r.Reader.Read(p)
}

func (r *sourceReader) Seek(offset int64, whence int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	return r.Reader.Seek(offset, whence)
}

func (r *sourceReader) Close() error {
	// Interrupt a missing-piece read before waiting for exclusive reader access.
	// The torrent reader's storage state does not support concurrent Read/Close.
	r.cancel()
	r.stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		r.err = r.Reader.Close()
	}
	return r.err
}

// MediaMIME deliberately excludes archives, playlists and torrent metadata.
func MediaMIME(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".mkv":
		return "video/x-matroska"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".ts", ".m2ts", ".mts":
		return "video/mp2t"
	case ".mpg", ".mpeg", ".vob":
		return "video/mpeg"
	case ".wmv":
		return "video/x-ms-wmv"
	case ".flv":
		return "video/x-flv"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".m4a":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".wav":
		return "audio/wav"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".opus":
		return "audio/opus"
	}
	t := mime.TypeByExtension(ext)
	if strings.HasPrefix(t, "video/") || strings.HasPrefix(t, "audio/") {
		return t
	}
	return ""
}
