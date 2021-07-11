package httphandlers

import (
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexballas/go2tv/internal/screeninterfaces"
	"github.com/alexballas/go2tv/internal/soapcalls"
	"github.com/pkg/errors"
)

// HTTPserver - new http.Server instance.
type HTTPserver struct {
	http *http.Server
	mux  *http.ServeMux
}

// ServeFiles - Start HTTP server and serve the files.
func (s *HTTPserver) ServeFiles(serverStarted chan<- struct{}, videoPath, subtitlesPath string,
	tvpayload *soapcalls.TVPayload, screen screeninterfaces.Screen) error {

	s.mux.HandleFunc("/"+filepath.Base(videoPath), s.serveVideoHandler(videoPath))
	s.mux.HandleFunc("/"+filepath.Base(subtitlesPath), s.serveSubtitlesHandler(subtitlesPath))
	s.mux.HandleFunc("/callback", s.callbackHandler(tvpayload, screen))

	ln, err := net.Listen("tcp", s.http.Addr)
	serverStarted <- struct{}{}
	if err != nil {
		return errors.Wrap(err, "Server Listen fail")
	}
	s.http.Serve(ln)
	return nil
}

func (s *HTTPserver) serveVideoHandler(video string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("transferMode.dlna.org", "Streaming")
		w.Header().Set("contentFeatures.dlna.org", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=017000 00000000000000000000000000")

		filePath, err := os.Open(video)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		defer filePath.Close()

		fileStat, err := filePath.Stat()
		if err != nil {
			http.NotFound(w, req)
			return
		}

		http.ServeContent(w, req, filepath.Base(video), fileStat.ModTime(), filePath)
	}
}

func (s *HTTPserver) serveSubtitlesHandler(subs string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("transferMode.dlna.org", "Streaming")
		w.Header().Set("contentFeatures.dlna.org", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=017000 00000000000000000000000000")

		filePath, err := os.Open(subs)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		defer filePath.Close()

		fileStat, err := filePath.Stat()
		if err != nil {
			http.NotFound(w, req)
			return
		}
		http.ServeContent(w, req, filepath.Base(subs), fileStat.ModTime(), filePath)
	}
}

func (s *HTTPserver) callbackHandler(tv *soapcalls.TVPayload, screen screeninterfaces.Screen) http.HandlerFunc {
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

		uuid := sidVal[0]
		uuid = strings.TrimLeft(uuid, "[")
		uuid = strings.TrimLeft(uuid, "]")
		uuid = strings.TrimPrefix(uuid, "uuid:")

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
			screeninterfaces.Emit(screen, "Playing")
		case "PAUSED_PLAYBACK":
			screeninterfaces.Emit(screen, "Paused")
		case "STOPPED":
			screeninterfaces.Emit(screen, "Stopped")
			tv.UnsubscribeSoapCall(uuid)
			screeninterfaces.Close(screen)
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
