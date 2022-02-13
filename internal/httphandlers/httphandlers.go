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
	"strings"
	"time"

	"github.com/alexballas/go2tv/internal/soapcalls"
	"github.com/alexballas/go2tv/internal/utils"
)

// HTTPserver - new http.Server instance.
type HTTPserver struct {
	http *http.Server
	mux  *http.ServeMux
}

// Screen interface.
type Screen interface {
	EmitMsg(string)
	Fini()
}

// Emit .
func Emit(scr Screen, s string) {
	scr.EmitMsg(s)
}

// Close .
func Close(scr Screen) {
	scr.Fini()
}

// ServeFiles - Start HTTP server and serve the files.
func (s *HTTPserver) ServeFiles(serverStarted chan<- struct{}, media, subtitles interface{},
	tvpayload *soapcalls.TVPayload, screen Screen) error {

	mURL, err := url.Parse(tvpayload.MediaURL)
	if err != nil {
		return fmt.Errorf("failed to parse MediaURL: %w", err)
	}

	sURL, err := url.Parse(tvpayload.SubtitlesURL)
	if err != nil {
		return fmt.Errorf("failed to parse SubtitlesURL: %w", err)
	}

	callbackURL, err := url.Parse(tvpayload.CallbackURL)
	if err != nil {
		return fmt.Errorf("failed to parse CallbackURL: %w", err)
	}

	s.mux.HandleFunc(mURL.Path, s.serveMediaHandler(tvpayload, media))
	s.mux.HandleFunc(sURL.Path, s.serveSubtitlesHandler(subtitles))
	s.mux.HandleFunc(callbackURL.Path, s.callbackHandler(tvpayload, screen))

	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("server listen error: %w", err)
	}

	serverStarted <- struct{}{}
	s.http.Serve(ln)

	return nil
}

func (s *HTTPserver) serveMediaHandler(tv *soapcalls.TVPayload, media interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		serveContent(w, req, tv, media, true)
	}
}

func (s *HTTPserver) serveSubtitlesHandler(subs interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		serveContent(w, req, nil, subs, false)
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

		// Apparently we should ignore the first message
		// On some media renderers we receive a STOPPED message
		// even before we start streaming.
		seq, err := soapcalls.GetSequence(uuid)
		if err != nil {
			http.NotFound(w, req)
			return
		}

		if seq == 0 {
			soapcalls.IncreaseSequence(uuid)
			fmt.Fprintf(w, "OK\n")
			return
		}

		reqParsedUnescape := html.UnescapeString(string(reqParsed))
		previousstate, newstate, err := soapcalls.EventNotifyParser(reqParsedUnescape)
		if err != nil {
			http.NotFound(w, req)
			return
		}

		if !soapcalls.UpdateMRstate(previousstate, newstate, uuid) {
			http.NotFound(w, req)
			return
		}

		switch newstate {
		case "PLAYING":
			Emit(screen, "Playing")
		case "PAUSED_PLAYBACK":
			Emit(screen, "Paused")
		case "STOPPED":
			Emit(screen, "Stopped")
			tv.UnsubscribeSoapCall(uuid)
			Close(screen)
		}
	}
}

// StopServeFiles .
func (s *HTTPserver) StopServeFiles() {
	s.http.Close()
}

// NewServer - create a new HTTP server.
func NewServer(a string) *HTTPserver {
	mux := http.NewServeMux()
	srv := HTTPserver{
		http: &http.Server{Addr: a, Handler: mux},
		mux:  mux,
	}

	return &srv
}

func serveContent(w http.ResponseWriter, r *http.Request, tv *soapcalls.TVPayload, s interface{}, isMedia bool) {
	respHeader := w.Header()
	if isMedia {
		respHeader["transferMode.dlna.org"] = []string{"Streaming"}
		respHeader["realTimeInfo.dlna.org"] = []string{"DLNA.ORG_TLAG=*"}
	} else {
		respHeader["transferMode.dlna.org"] = []string{"Interactive"}
	}

	var mediaType string
	if tv != nil {
		mediaType = tv.MediaType
	}

	switch f := s.(type) {
	case string:
		if r.Header.Get("getcontentFeatures.dlna.org") == "1" {
			contentFeatures, err := utils.BuildContentFeatures(mediaType, "01", false)
			if err != nil {
				http.NotFound(w, r)
				return
			}

			respHeader["contentFeatures.dlna.org"] = []string{contentFeatures}
		}

		filePath, err := os.Open(f)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer filePath.Close()

		fileStat, err := filePath.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}

		name := strings.TrimLeft(r.URL.Path, "/")
		http.ServeContent(w, r, name, fileStat.ModTime(), filePath)

	case []byte:
		if r.Header.Get("getcontentFeatures.dlna.org") == "1" {
			contentFeatures, err := utils.BuildContentFeatures(mediaType, "01", false)
			if err != nil {
				http.NotFound(w, r)
				return
			}

			respHeader["contentFeatures.dlna.org"] = []string{contentFeatures}
		}

		bReader := bytes.NewReader(f)

		name := strings.TrimLeft(r.URL.Path, "/")
		http.ServeContent(w, r, name, time.Now(), bReader)

	case io.ReadCloser:
		if r.Header.Get("getcontentFeatures.dlna.org") == "1" {
			contentFeatures, err := utils.BuildContentFeatures(mediaType, "00", false)
			if err != nil {
				http.NotFound(w, r)
				return
			}

			respHeader["contentFeatures.dlna.org"] = []string{contentFeatures}
		}

		// No seek support
		if r.Method == http.MethodGet {
			io.Copy(w, f)
			f.Close()
		} else {
			w.WriteHeader(http.StatusOK)
		}

	default:
		http.NotFound(w, r)
		return
	}
}
