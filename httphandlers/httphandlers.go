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

	"github.com/alexballas/go2tv/soapcalls"
	"github.com/alexballas/go2tv/utils"
)

// HTTPserver - new http.Server instance.
type HTTPserver struct {
	http *http.Server
	mux  *http.ServeMux
}

// Screen interface is used to push message back to the user
// as these are returned by the callback requests.
type Screen interface {
	EmitMsg(string)
	Fini()
}

// StartServer will start a HTTP server to serve the selected media files and
// also handle the subscriptions requests from the DMR devices.
func (s *HTTPserver) StartServer(serverStarted chan<- struct{}, media, subtitles interface{},
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
		seq, err := tv.GetSequence(uuid)
		if err != nil {
			http.NotFound(w, req)
			return
		}

		if seq == 0 {
			tv.IncreaseSequence(uuid)
			fmt.Fprintf(w, "OK\n")
			return
		}

		reqParsedUnescape := html.UnescapeString(string(reqParsed))
		previousstate, newstate, err := soapcalls.EventNotifyParser(reqParsedUnescape)
		if err != nil {
			http.NotFound(w, req)
			return
		}

		if !tv.UpdateMRstate(previousstate, newstate, uuid) {
			http.NotFound(w, req)
			return
		}

		switch newstate {
		case "PLAYING":
			screen.EmitMsg("Playing")
		case "PAUSED_PLAYBACK":
			screen.EmitMsg("Paused")
		case "STOPPED":
			screen.EmitMsg("Stopped")
			tv.UnsubscribeSoapCall(uuid)
			screen.Fini()
		}
	}
}

// StopServer forcefully closes the HTTP server.
func (s *HTTPserver) StopServer() {
	s.http.Close()
}

// NewServer constractor generates a new HTTPserver type.
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

	respHeader["transferMode.dlna.org"] = []string{"Interactive"}

	if isMedia {
		respHeader["transferMode.dlna.org"] = []string{"Streaming"}
		respHeader["realTimeInfo.dlna.org"] = []string{"DLNA.ORG_TLAG=*"}
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
		}

		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
		return
	}
}
