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
func (s *HTTPserver) ServeFiles(serverStarted chan<- struct{}, mediaPath, subtitlesPath string,
	tvpayload *soapcalls.TVPayload, screen Screen) error {

	s.mux.HandleFunc("/"+filepath.Base(mediaPath), s.serveMediaHandler(mediaPath))
	s.mux.HandleFunc("/"+filepath.Base(subtitlesPath), s.serveSubtitlesHandler(subtitlesPath))
	s.mux.HandleFunc("/callback", s.callbackHandler(tvpayload, screen))

	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("server listen error: %w", err)
	}

	serverStarted <- struct{}{}
	s.http.Serve(ln)
	return nil
}

func (s *HTTPserver) serveMediaHandler(media string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		respHeader := w.Header()
		respHeader["transferMode.dlna.org"] = []string{"Streaming"}
		respHeader["realTimeInfo.dlna.org"] = []string{"DLNA.ORG_TLAG=*"}

		contentFeatures, err := utils.BuildContentFeatures(media)
		if err != nil {
			http.NotFound(w, req)
			return
		}

		if req.Header.Get("getcontentFeatures.dlna.org") == "1" {
			respHeader["contentFeatures.dlna.org"] = []string{contentFeatures}
		}

		filePath, err := os.Open(media)
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

		http.ServeContent(w, req, filepath.Base(media), fileStat.ModTime(), filePath)
	}
}

func (s *HTTPserver) serveSubtitlesHandler(subs string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		respHeader := w.Header()
		respHeader["transferMode.dlna.org"] = []string{"Interactive"}

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
