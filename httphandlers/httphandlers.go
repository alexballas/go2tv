package httphandlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

// HTTPserver - new http.Server instance.
type HTTPserver struct {
	http *http.Server
	Mux  *http.ServeMux
	// Chromecast runs one ffmpeg command at a time per server. DLNA commands
	// are request-owned so a renderer reconnect cannot corrupt another command.
	ffmpeg      *exec.Cmd
	handlers    map[string]handler
	dirHandlers map[string]string // Handlers for serving entire directories (e.g. HLS)
	mu          sync.Mutex
}

// handler holds the configuration for a registered media path.
// For DLNA: payload is set, transcode is nil
// For Chromecast with transcoding: transcode is set, payload is nil
// For Chromecast without transcoding: both are nil
type handler struct {
	payload   *soapcalls.TVPayload    // For DLNA (may be nil for Chromecast)
	transcode *utils.TranscodeOptions // For Chromecast transcoding (may be nil)
	media     any
	mediaType string
	static    bool
}

// Screen interface is used to push message back to the user
// as these are returned by the subscriptions.
type Screen interface {
	EmitMsg(string)
	Fini()
	SetMediaType(string)
}

// We use this type to be able to test
// the serveContent function without the
// need of os.Open in the tests.
type osFileType struct {
	time time.Time
	file io.ReadSeeker
	path string
}

// AddHandler dynamically adds a new handler. Currently used by the gapless playback logic where we use
// the same server to serve multiple media files.
// For DLNA: pass payload, transcode=nil
// For Chromecast with transcoding: pass payload=nil, transcode options
// For Chromecast without transcoding: pass both as nil
func (s *HTTPserver) AddHandler(path string, payload *soapcalls.TVPayload, transcode *utils.TranscodeOptions, media any) {
	s.mu.Lock()
	s.handlers[path] = handler{payload: payload, transcode: transcode, media: media}
	s.mu.Unlock()
}

// AddStaticHandler adds GET/HEAD static content with explicit MIME and CORS.
// media may be a file path, []byte, or MediaReaderSeeker.
func (s *HTTPserver) AddStaticHandler(path, mediaType string, media any) {
	s.mu.Lock()
	s.handlers[path] = handler{media: media, mediaType: mediaType, static: true}
	s.mu.Unlock()
}

// RemoveHandler dynamically removes a handler.
func (s *HTTPserver) RemoveHandler(path string) {
	s.mu.Lock()
	delete(s.handlers, path)
	s.mu.Unlock()
}

// AddDirectoryHandler registers a directory to be served under a URL prefix.
// Used for HLS serving where multiple .ts files are requested.
func (s *HTTPserver) AddDirectoryHandler(urlPrefix, fsPath string) {
	s.mu.Lock()
	s.dirHandlers[urlPrefix] = fsPath
	s.mu.Unlock()
}

// RemoveDirectoryHandler removes a directory handler.
func (s *HTTPserver) RemoveDirectoryHandler(urlPrefix string) {
	s.mu.Lock()
	delete(s.dirHandlers, urlPrefix)
	s.mu.Unlock()
}

// GetAddr returns the server's listen address (ip:port).
func (s *HTTPserver) GetAddr() string {
	return s.http.Addr
}

// StartSimpleServerWithTranscode starts HTTP server with optional transcoding.
// Used by Chromecast when media needs transcoding.
// Pass tcOpts=nil for direct streaming (no transcoding).
func (s *HTTPserver) StartSimpleServerWithTranscode(
	serverStarted chan<- error,
	mediaPath string,
	tcOpts *utils.TranscodeOptions,
) {
	// Register media handler
	// Use filepath.Base because r.URL.Path is already URL-decoded by Go's HTTP server
	mediaFilename := "/" + filepath.Base(mediaPath)
	s.AddHandler(mediaFilename, nil, tcOpts, mediaPath)

	s.Mux.HandleFunc("/", s.ServeMediaHandler())

	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		serverStarted <- fmt.Errorf("server listen error: %w", err)
		return
	}

	serverStarted <- nil
	_ = s.http.Serve(ln)
}

// StartServing starts the HTTP server after handlers have been added via AddHandler.
// Used by mobile Chromecast which adds handlers separately with io.ReadCloser media.
func (s *HTTPserver) StartServing(serverStarted chan<- error) {
	s.Mux.HandleFunc("/", s.ServeMediaHandler())

	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		serverStarted <- fmt.Errorf("server listen error: %w", err)
		return
	}

	serverStarted <- nil
	_ = s.http.Serve(ln)
}

// StartServer will start a HTTP server to serve the selected media files and
// also handle the subscriptions requests from the DMR devices.
func (s *HTTPserver) StartServer(serverStarted chan<- error, media, subtitles any,
	tvpayload *soapcalls.TVPayload, screen Screen,
) {
	mURL, err := url.Parse(tvpayload.MediaURL)
	if err != nil {
		serverStarted <- fmt.Errorf("failed to parse MediaURL: %w", err)
		return
	}

	sURL, err := url.Parse(tvpayload.SubtitlesURL)
	if err != nil {
		serverStarted <- fmt.Errorf("failed to parse SubtitlesURL: %w", err)
		return
	}

	// Dynamically add handlers to better support gapless playback where we're
	// required to serve new files with our existing HTTP server.
	s.AddHandler(mURL.Path, tvpayload, nil, media)

	if sURL.Path != "/." && !tvpayload.Transcode {
		s.AddHandler(sURL.Path, nil, nil, subtitles)
	}

	callbackURL, err := url.Parse(tvpayload.CallbackURL)
	if err != nil {
		serverStarted <- fmt.Errorf("failed to parse CallbackURL: %w", err)
		return
	}

	s.Mux.HandleFunc("/", s.ServeMediaHandler())
	s.Mux.HandleFunc(callbackURL.Path, s.callbackHandler(tvpayload, screen))

	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		serverStarted <- fmt.Errorf("server listen error: %w", err)
		return
	}

	serverStarted <- nil
	_ = s.http.Serve(ln)
}

// ServeMediaHandler is a helper method used to properly handle media and subtitle streaming.
func (s *HTTPserver) ServeMediaHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestPathLower := strings.ToLower(r.URL.Path)

		// Add CORS headers for subtitle files (needed for Chromecast)
		if strings.HasSuffix(requestPathLower, ".vtt") || strings.HasSuffix(requestPathLower, ".srt") {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

			// Handle OPTIONS preflight request
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusOK)
				return
			}
		}

		s.mu.Lock()
		out, exists := s.handlers[r.URL.Path]
		if !exists {
			// Check for directory handlers
			for prefix, dirPath := range s.dirHandlers {
				if after, ok := strings.CutPrefix(r.URL.Path, prefix); ok {
					// Found a match. Build candidate path and enforce it stays under dirPath.
					relPath := filepath.Clean(strings.TrimPrefix(after, "/"))
					baseAbs, err := filepath.Abs(filepath.Clean(dirPath))
					if err != nil {
						continue
					}

					fullAbs, err := filepath.Abs(filepath.Join(baseAbs, relPath))
					if err != nil {
						continue
					}

					relToBase, err := filepath.Rel(baseAbs, fullAbs)
					if err != nil {
						continue
					}

					if relToBase == ".." ||
						strings.HasPrefix(relToBase, ".."+string(filepath.Separator)) ||
						filepath.IsAbs(relToBase) {
						continue
					}

					out = handler{media: fullAbs}
					exists = true
					break
				}
			}
		}
		s.mu.Unlock()

		if !exists {
			http.Error(w, "not exists", http.StatusNotFound)
			return
		}

		if out.static {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Content-Type", out.mediaType)
			if etag := artworkETag(r.URL.Path); etag != "" {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				w.Header().Set("ETag", etag)
			}
			switch r.Method {
			case http.MethodOptions:
				w.WriteHeader(http.StatusOK)
				return
			case http.MethodGet, http.MethodHead:
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
		}

		// Explicitly set Content-Type for HLS files.
		if !out.static {
			if strings.HasSuffix(requestPathLower, ".m3u8") {
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			} else if strings.HasSuffix(requestPathLower, ".ts") {
				w.Header().Set("Content-Type", "video/mp2t")
			} else if strings.HasSuffix(requestPathLower, ".mp4") || strings.HasSuffix(requestPathLower, ".m4s") {
				w.Header().Set("Content-Type", "video/mp4")
			}
		}

		switch f := out.media.(type) {
		case string:
			m, err := os.Open(f)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer m.Close()

			info, err := m.Stat()
			if err != nil {
				http.NotFound(w, r)
				return
			}

			out.media = osFileType{
				time: info.ModTime(),
				file: m,
				path: f,
			}
		}

		serveContent(w, r, out.payload, out.transcode, out.media, s.ffmpeg)
	}
}

func artworkETag(requestPath string) string {
	const (
		artworkPrefix = "/artwork/"
		sha256Length  = 64
	)

	if !strings.HasPrefix(requestPath, artworkPrefix) || !strings.HasSuffix(requestPath, ".jpg") {
		return ""
	}
	id := strings.TrimSuffix(strings.TrimPrefix(requestPath, artworkPrefix), ".jpg")
	if len(id) != sha256Length {
		return ""
	}
	for _, char := range id {
		if !('0' <= char && char <= '9' || 'a' <= char && char <= 'f') {
			return ""
		}
	}
	return `"` + id + `"`
}

func (s *HTTPserver) callbackHandler(tv *soapcalls.TVPayload, screen Screen) http.HandlerFunc {
	sink := &legacyScreenSink{tv: tv, screen: screen}
	return func(w http.ResponseWriter, req *http.Request) {
		reqParsed, _ := io.ReadAll(req.Body)
		sidVal, sidExists := req.Header["Sid"]

		if !sidExists || (len(sidVal) > 0 && sidVal[0] == "") {
			http.NotFound(w, req)
			return
		}

		uuid := strings.TrimPrefix(sidVal[0], "uuid:")

		reqParsedUnescape := html.UnescapeString(string(reqParsed))
		event, err := soapcalls.ParseEventNotify(reqParsedUnescape)
		if err != nil {
			http.NotFound(w, req)
			return
		}

		sink.HandleCallbackEvent(req.Context(), CallbackEvent{SID: uuid, Source: req.RemoteAddr, TransportState: event.TransportState, MediaType: tv.MediaType})
	}
}

// legacyScreenSink keeps existing GUI/CLI behavior outside HTTP parsing.
type legacyScreenSink struct {
	tv     *soapcalls.TVPayload
	screen Screen
}

func (s *legacyScreenSink) HandleCallbackEvent(_ context.Context, event CallbackEvent) {
	processStop, err := s.tv.GetProcessStop(event.SID)
	if err != nil {
		return
	}
	if !processStop && event.TransportState == "STOPPED" {
		s.tv.SetProcessStopTrue(event.SID)
		return
	}
	if !s.tv.UpdateMRstate(event.TransportState, event.SID) {
		return
	}
	switch event.TransportState {
	case "PLAYING":
		if event.MediaType != "" {
			s.screen.SetMediaType(event.MediaType)
		}
		s.screen.EmitMsg("Playing")
		s.tv.SetProcessStopTrue(event.SID)
	case "PAUSED_PLAYBACK":
		s.screen.EmitMsg("Paused")
	case "STOPPED":
		s.screen.EmitMsg("Stopped")
		_ = s.tv.UnsubscribeSoapCall(event.SID)
		s.screen.Fini()
	}
}

// AddHLSHandler configures the server to serve HLS content from a directory
func (s *HTTPserver) AddHLSHandler(urlPrefix, dir string) {
	fileServer := http.FileServer(http.Dir(dir))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPathLower := strings.ToLower(r.URL.Path)

		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		if strings.HasSuffix(requestPathLower, ".m3u8") {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		} else if strings.HasSuffix(requestPathLower, ".ts") {
			w.Header().Set("Content-Type", "video/mp2t")
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		} else if strings.HasSuffix(requestPathLower, ".mp4") || strings.HasSuffix(requestPathLower, ".m4s") {
			w.Header().Set("Content-Type", "video/mp4")
		}

		fileServer.ServeHTTP(w, r)
	})

	s.Mux.Handle(urlPrefix, http.StripPrefix(urlPrefix, handler))
}

// StopServer forcefully closes the HTTP server.
func (s *HTTPserver) StopServer() {
	if s.ffmpeg != nil && s.ffmpeg.Process != nil {
		_ = s.ffmpeg.Process.Kill()
	}

	s.http.Close()
}

// NewServer constractor generates a new HTTPserver type.
func NewServer(a string) *HTTPserver {
	mux := http.NewServeMux()
	srv := HTTPserver{
		http:        &http.Server{Addr: a, Handler: mux},
		Mux:         mux,
		ffmpeg:      new(exec.Cmd),
		handlers:    make(map[string]handler),
		dirHandlers: make(map[string]string),
	}

	return &srv
}

func serveContent(w http.ResponseWriter, r *http.Request, tv *soapcalls.TVPayload, tcOpts *utils.TranscodeOptions, mf any, ff *exec.Cmd) {
	var (
		isMedia   bool
		transcode bool
		seek      bool
		mediaType string
	)

	if tv != nil {
		isMedia = true
		transcode = tv.Transcode
		mediaType = utils.DLNAResourceMediaType(tv.MediaType, transcode)
		seek = tv.Seekable
		tv.Log().Debug("", "Method", "DLNAMediaHTTP", "Action", "Request", "HTTP Method", r.Method, "Path", r.URL.Path,
			"TimeSeekRange", r.Header.Get("TimeSeekRange.dlna.org"), "Range", r.Header.Get("Range"),
			"GetContentFeatures", r.Header.Get("getcontentFeatures.dlna.org"),
			"GetAvailableSeekRange", r.Header.Get("getAvailableSeekRange.dlna.org"))
	}

	// Chromecast transcoding takes precedence
	if tcOpts != nil {
		isMedia = true
		transcode = true
		mediaType = "video/mp4" // Chromecast transcoding outputs fragmented MP4
	}

	w.Header()["transferMode.dlna.org"] = []string{utils.DLNATransferMode(mediaType)}

	if isMedia {
		w.Header()["realTimeInfo.dlna.org"] = []string{"DLNA.ORG_TLAG=*"}
		w.Header()["Content-Type"] = []string{mediaType}
	}

	switch f := mf.(type) {
	case osFileType:
		serveContentCustomType(w, r, tv, tcOpts, mediaType, transcode, seek, f, ff)
	case MediaReaderSeeker:
		rsc, err := f()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if transcode {
			serveContentReadClose(w, r, tv, tcOpts, mediaType, transcode, rsc, ff)
			return
		}
		serveContentSeekCloser(w, r, mediaType, seek, rsc)
	case []byte:
		serveContentBytes(w, r, mediaType, f)
	case LiveStream:
		// A probe must not take the reader, or the GET that follows it finds
		// the stream busy and the renderer never starts playing.
		if r.Method != http.MethodGet {
			serveContentReadClose(w, r, tv, tcOpts, mediaType, transcode, http.NoBody, ff)
			return
		}

		rc, err := f()
		if err != nil {
			http.Error(w, "stream busy", http.StatusServiceUnavailable)
			return
		}
		serveContentReadClose(w, r, tv, tcOpts, mediaType, transcode, rc, ff)
	case io.ReadCloser:
		serveContentReadClose(w, r, tv, tcOpts, mediaType, transcode, f, ff)
	default:
		http.NotFound(w, r)
		return
	}
}

func serveContentBytes(w http.ResponseWriter, r *http.Request, mediaType string, f []byte) {
	// Add CORS for subtitle files (needed for Chromecast)
	requestPathLower := strings.ToLower(r.URL.Path)
	if strings.HasSuffix(requestPathLower, ".vtt") || strings.HasSuffix(requestPathLower, ".srt") {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}

	if r.Header.Get("getcontentFeatures.dlna.org") == "1" {
		w.Header()["contentFeatures.dlna.org"] = []string{utils.BuildDLNAContentFeatures(utils.DLNAContentFeaturesOptions{ByteSeek: true})}
	}

	bReader := bytes.NewReader(f)
	name := strings.TrimLeft(r.URL.Path, "/")
	http.ServeContent(w, r, name, time.Now(), bReader)
}

func serveContentReadClose(w http.ResponseWriter, r *http.Request, tv *soapcalls.TVPayload, tcOpts *utils.TranscodeOptions, mediaType string, transcode bool, f io.ReadCloser, ff *exec.Cmd) {
	defer f.Close()

	if r.Header.Get("getcontentFeatures.dlna.org") == "1" {
		w.Header()["contentFeatures.dlna.org"] = []string{utils.BuildDLNAContentFeatures(utils.DLNAContentFeaturesOptions{Converted: transcode})}
	}
	// In ffmpeg we can emulate seek support for live streams
	if transcode && r.Method == http.MethodGet && strings.Contains(mediaType, "video") {
		// Route based on which config is provided
		switch {
		case tcOpts != nil:
			// Chromecast transcoding (fragmented MP4)
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			err := utils.ServeChromecastTranscodedStream(r.Context(), w, f, ff, tcOpts)
			if err != nil {
				if errors.Is(err, utils.ErrTranscodeBusy) {
					http.Error(w, "busy", http.StatusServiceUnavailable)
					return
				}
				tcOpts.LogError("serveContentReadClose", "ChromecastTranscode", err)
			}
		case tv != nil:
			// DLNA transcoding (MPEGTS)
			var command exec.Cmd
			err := utils.ServeTranscodedStream(r.Context(), w, f, &command, tv.FFmpegPath, tv.FFmpegSubsPath, tv.FFmpegSeek, utils.SubtitleSizeMedium)
			if err != nil {
				tv.Log().Error("", "function", "serveContentReadClose", "Action", "Transcode", "error", err)
			}
		}
		return
	}

	// No seek support
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, f)
	}
}

// LiveStream borrows the reader of a live, non-seekable stream for the
// duration of one request. Unlike MediaReaderSeeker it cannot hand out a fresh
// reader per request, because there is only one stream: concurrent readers
// would split the packets between them and corrupt both. Implementations
// return an error while the stream is lent out, and the request is answered
// with 503 rather than a garbled body. Closing the returned reader returns the
// borrow; whether that also stops the producer is the implementation's call.
type LiveStream func() (io.ReadCloser, error)

// MediaReaderSeeker returns a fresh seekable reader for the media on each call.
// It is used on mobile, where a content:// URI can provide a seekable file
// descriptor (via refyne storage.ReaderSeeker) but a new descriptor must be
// opened per HTTP request, since each request has its own read offset.
type MediaReaderSeeker func() (io.ReadSeekCloser, error)

// serveContentSeekCloser serves a seekable reader directly via
// http.ServeContent, giving full HTTP range-request support without copying the
// media to a temporary file. The reader is closed once the request completes.
func serveContentSeekCloser(w http.ResponseWriter, r *http.Request, mediaType string, seek bool, f io.ReadSeekCloser) {
	defer f.Close()

	if r.Header.Get("getcontentFeatures.dlna.org") == "1" {
		w.Header()["contentFeatures.dlna.org"] = []string{utils.BuildDLNAContentFeatures(utils.DLNAContentFeaturesOptions{ByteSeek: seek})}
	}

	name := strings.TrimLeft(r.URL.Path, "/")
	http.ServeContent(w, r, name, time.Now(), f)
}

func serveContentCustomType(w http.ResponseWriter, r *http.Request, tv *soapcalls.TVPayload, tcOpts *utils.TranscodeOptions, mediaType string, transcode, seek bool, f osFileType, ff *exec.Cmd) {
	if r.Header.Get("getcontentFeatures.dlna.org") == "1" {
		w.Header()["contentFeatures.dlna.org"] = []string{utils.BuildDLNAContentFeatures(utils.DLNAContentFeaturesOptions{
			ByteSeek:  seek && !transcode,
			Converted: transcode,
		})}
	}
	if transcode && r.Method == http.MethodGet && strings.Contains(mediaType, "video") {
		// Since we're dealing with an io.Reader we can't
		// allow any HEAD requests that some DMRs trigger.
		var input any = f.file
		// The only case where we should expect f.path to be ""
		// is only during our unit tests where we emulate the files.
		if f.path != "" {
			input = f.path
		}

		// Route based on which config is provided
		switch {
		case tcOpts != nil:
			// Chromecast transcoding (fragmented MP4)
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			err := utils.ServeChromecastTranscodedStream(r.Context(), w, input, ff, tcOpts)
			if err != nil {
				if errors.Is(err, utils.ErrTranscodeBusy) {
					http.Error(w, "busy", http.StatusServiceUnavailable)
					return
				}
				tcOpts.LogError("serveContentCustomType", "ChromecastTranscode", err)
			}
		case tv != nil:
			// DLNA transcoding (MPEGTS)
			var command exec.Cmd
			err := utils.ServeTranscodedStream(r.Context(), w, input, &command, tv.FFmpegPath, tv.FFmpegSubsPath, tv.FFmpegSeek, utils.SubtitleSizeMedium)
			if err != nil {
				tv.Log().Error("", "function", "serveContentCustomType", "Action", "Transcode", "error", err)
			}
		}
		return
	}

	name := strings.TrimLeft(r.URL.Path, "/")

	if r.Method == http.MethodGet {
		http.ServeContent(w, r, name, f.time, f.file)
	}

	if r.Method == http.MethodHead {
		size, err := f.file.Seek(0, io.SeekEnd)
		if err != nil {
			http.Error(w, "cant get file size", 500)
		}
		_, err = f.file.Seek(0, io.SeekStart)
		if err != nil {
			http.Error(w, "cant get file size", 500)
		}

		w.Header()["Content-Length"] = []string{strconv.FormatInt(size, 10)}

		if !f.time.IsZero() && !f.time.Equal(time.Unix(0, 0)) {
			w.Header().Set("Last-Modified", f.time.UTC().Format(http.TimeFormat))
		}
	}
}
