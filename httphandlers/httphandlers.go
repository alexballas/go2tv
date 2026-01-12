package httphandlers

import (
	"bytes"
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

	"github.com/alexballas/go2tv/soapcalls"
	"github.com/alexballas/go2tv/utils"
)

// HTTPserver - new http.Server instance.
type HTTPserver struct {
	http *http.Server
	Mux  *http.ServeMux
	// We only need to run one ffmpeg
	// command at a time, per server instance
	ffmpeg   *exec.Cmd
	handlers map[string]handler
	mu       sync.Mutex
}

// handler holds the configuration for a registered media path.
// For DLNA: payload is set, transcode is nil
// For Chromecast with transcoding: transcode is set, payload is nil
// For Chromecast without transcoding: both are nil
type handler struct {
	payload   *soapcalls.TVPayload    // For DLNA (may be nil for Chromecast)
	transcode *utils.TranscodeOptions // For Chromecast transcoding (may be nil)
	media     any
}

// Screen interface is used to push message back to the user
// as these are returned by the subscriptions.
type Screen interface {
	EmitMsg(string)
	Fini()
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

// RemoveHandler dynamically removes a handler.
func (s *HTTPserver) RemoveHandler(path string) {
	s.mu.Lock()
	delete(s.handlers, path)
	s.mu.Unlock()
}

// GetAddr returns the server's listen address (ip:port).
func (s *HTTPserver) GetAddr() string {
	return s.http.Addr
}

// StartSimpleServer starts a minimal HTTP server for serving media files.
// Used by Chromecast which doesn't need DLNA callback handlers or TVPayload.
func (s *HTTPserver) StartSimpleServer(serverStarted chan<- error, mediaPath string) {
	s.StartSimpleServerWithTranscode(serverStarted, mediaPath, nil)
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
		// Add CORS headers for subtitle files (needed for Chromecast)
		if strings.HasSuffix(r.URL.Path, ".vtt") || strings.HasSuffix(r.URL.Path, ".srt") {
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
		s.mu.Unlock()

		if !exists {
			http.Error(w, "not exists", http.StatusNotFound)
			return
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

func (s *HTTPserver) callbackHandler(tv *soapcalls.TVPayload, screen Screen) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		reqParsed, _ := io.ReadAll(req.Body)
		sidVal, sidExists := req.Header["Sid"]

		if !sidExists {
			http.NotFound(w, req)
			return
		}

		if sidVal[0] == "" {
			http.NotFound(w, req)
			return
		}

		uuid := strings.TrimPrefix(sidVal[0], "uuid:")

		reqParsedUnescape := html.UnescapeString(string(reqParsed))
		previousstate, newstate, err := soapcalls.EventNotifyParser(reqParsedUnescape)

		if err != nil {
			http.NotFound(w, req)
			return
		}

		// Apparently we should ignore the first message
		// On some media renderers we receive a STOPPED message
		// even before we start streaming.
		processStop, err := tv.GetProcessStop(uuid)
		if err != nil {
			http.NotFound(w, req)
			return
		}

		if !processStop && newstate == "STOPPED" {
			tv.SetProcessStopTrue(uuid)
			fmt.Fprintf(w, "OK\n")
			return
		}

		if !tv.UpdateMRstate(previousstate, newstate, uuid) {
			http.NotFound(w, req)
			return
		}

		switch newstate {
		case "PLAYING":
			screen.EmitMsg("Playing")
			tv.SetProcessStopTrue(uuid)
		case "PAUSED_PLAYBACK":
			screen.EmitMsg("Paused")
		case "STOPPED":
			screen.EmitMsg("Stopped")
			_ = tv.UnsubscribeSoapCall(uuid)
			screen.Fini()
		}
	}
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
		http:     &http.Server{Addr: a, Handler: mux},
		Mux:      mux,
		ffmpeg:   new(exec.Cmd),
		handlers: make(map[string]handler),
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
		mediaType = tv.MediaType
		seek = tv.Seekable
	}

	// Chromecast transcoding takes precedence
	if tcOpts != nil {
		isMedia = true
		transcode = true
		mediaType = "video/mp4" // Chromecast transcoding outputs fragmented MP4
	}

	w.Header()["transferMode.dlna.org"] = []string{"Interactive"}

	if isMedia {
		w.Header()["transferMode.dlna.org"] = []string{"Streaming"}
		w.Header()["realTimeInfo.dlna.org"] = []string{"DLNA.ORG_TLAG=*"}
		w.Header()["Content-Type"] = []string{mediaType}
	}

	switch f := mf.(type) {
	case osFileType:
		serveContentCustomType(w, r, tv, tcOpts, mediaType, transcode, seek, f, ff)
	case []byte:
		serveContentBytes(w, r, mediaType, f)
	case io.ReadCloser:
		serveContentReadClose(w, r, tv, tcOpts, mediaType, transcode, f, ff)
	default:
		http.NotFound(w, r)
		return
	}
}

func serveContentBytes(w http.ResponseWriter, r *http.Request, mediaType string, f []byte) {
	// Add CORS for subtitle files (needed for Chromecast)
	if strings.HasSuffix(r.URL.Path, ".vtt") || strings.HasSuffix(r.URL.Path, ".srt") {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}

	if r.Header.Get("getcontentFeatures.dlna.org") == "1" {
		contentFeatures, err := utils.BuildContentFeatures(mediaType, "01", false)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		w.Header()["contentFeatures.dlna.org"] = []string{contentFeatures}
	}

	bReader := bytes.NewReader(f)
	name := strings.TrimLeft(r.URL.Path, "/")
	http.ServeContent(w, r, name, time.Now(), bReader)
}

func serveContentReadClose(w http.ResponseWriter, r *http.Request, tv *soapcalls.TVPayload, tcOpts *utils.TranscodeOptions, mediaType string, transcode bool, f io.ReadCloser, ff *exec.Cmd) {
	if r.Header.Get("getcontentFeatures.dlna.org") == "1" {
		contentFeatures, err := utils.BuildContentFeatures(mediaType, "00", transcode)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		w.Header()["contentFeatures.dlna.org"] = []string{contentFeatures}
	}

	// Since we're dealing with an io.Reader we can't
	// allow any HEAD requests that some DMRs trigger.
	if transcode && r.Method == http.MethodGet && strings.Contains(mediaType, "video") {
		// Route based on which config is provided
		switch {
		case tcOpts != nil:
			// Chromecast transcoding (fragmented MP4)
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			err := utils.ServeChromecastTranscodedStream(r.Context(), w, f, ff, tcOpts)
			if err != nil {
				tcOpts.LogError("serveContentReadClose", "ChromecastTranscode", err)
			}
		case tv != nil:
			// DLNA transcoding (MPEGTS)
			err := utils.ServeTranscodedStream(r.Context(), w, f, ff, tv.FFmpegPath, tv.FFmpegSubsPath, tv.FFmpegSeek, utils.SubtitleSizeMedium)
			if err != nil {
				tv.Log().Error().Str("function", "serveContentReadClose").Str("Action", "Transcode").Err(err).Msg("")
			}
		}
		return
	}

	// No seek support
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, f)
		f.Close()
		return
	}
}

func serveContentCustomType(w http.ResponseWriter, r *http.Request, tv *soapcalls.TVPayload, tcOpts *utils.TranscodeOptions, mediaType string, transcode, seek bool, f osFileType, ff *exec.Cmd) {
	if r.Header.Get("getcontentFeatures.dlna.org") == "1" {
		seekflag := "00"
		if seek {
			seekflag = "01"
		}

		contentFeatures, err := utils.BuildContentFeatures(mediaType, seekflag, transcode)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		w.Header()["contentFeatures.dlna.org"] = []string{contentFeatures}
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
				tcOpts.LogError("serveContentCustomType", "ChromecastTranscode", err)
			}
		case tv != nil:
			// DLNA transcoding (MPEGTS)
			err := utils.ServeTranscodedStream(r.Context(), w, input, ff, tv.FFmpegPath, tv.FFmpegSubsPath, tv.FFmpegSeek, utils.SubtitleSizeMedium)
			if err != nil {
				tv.Log().Error().Str("function", "serveContentCustomType").Str("Action", "Transcode").Err(err).Msg("")
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
