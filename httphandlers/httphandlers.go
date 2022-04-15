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
	"strings"
	"time"

	"github.com/alexballas/go2tv/soapcalls"
	"github.com/alexballas/go2tv/utils"
)

// HTTPserver - new http.Server instance.
type HTTPserver struct {
	http *http.Server
	mux  *http.ServeMux
	// We only need to run one ffmpeg
	// command at a time, per server instance
	ffmpeg *exec.Cmd
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
}

// StartServer will start a HTTP server to serve the selected media files and
// also handle the subscriptions requests from the DMR devices.
func (s *HTTPserver) StartServer(serverStarted chan<- struct{}, media, subtitles interface{},
	tvpayload *soapcalls.TVPayload, screen Screen,
) error {
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
	s.mux.HandleFunc(sURL.Path, s.serveMediaHandler(nil, subtitles))
	s.mux.HandleFunc(callbackURL.Path, s.callbackHandler(tvpayload, screen))

	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("server listen error: %w", err)
	}

	serverStarted <- struct{}{}
	_ = s.http.Serve(ln)

	return nil
}

func (s *HTTPserver) serveMediaHandler(tv *soapcalls.TVPayload, media interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var media2 interface{}

		switch f := media.(type) {
		case string:
			m, err := os.Open(f)
			if err != nil {
				http.NotFound(w, req)
				return
			}
			defer m.Close()

			info, err := m.Stat()
			if err != nil {
				http.NotFound(w, req)
				return
			}

			media2 = osFileType{
				time: info.ModTime(),
				file: m,
			}
		}

		serveContent(w, req, tv, media2, s.ffmpeg)
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
			_ = tv.UnsubscribeSoapCall(uuid)
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
		http:   &http.Server{Addr: a, Handler: mux},
		mux:    mux,
		ffmpeg: new(exec.Cmd),
	}

	return &srv
}

func serveContent(w http.ResponseWriter, r *http.Request, tv *soapcalls.TVPayload, mf interface{}, ff *exec.Cmd) {
	var isMedia bool
	var transcode bool
	var mediaType string

	if tv != nil {
		isMedia = true
		transcode = tv.Transcode
		mediaType = tv.MediaType
	}

	respHeader := w.Header()

	respHeader["transferMode.dlna.org"] = []string{"Interactive"}

	if isMedia {
		respHeader["transferMode.dlna.org"] = []string{"Streaming"}
		respHeader["realTimeInfo.dlna.org"] = []string{"DLNA.ORG_TLAG=*"}
	}

	switch f := mf.(type) {
	case osFileType:
		if r.Header.Get("getcontentFeatures.dlna.org") == "1" {

			// comments
			seek := "01"
			if strings.Contains(mediaType, "video") && transcode {
				seek = "00"
			}

			contentFeatures, err := utils.BuildContentFeatures(mediaType, seek, transcode)
			if err != nil {
				http.NotFound(w, r)
				return
			}

			respHeader["contentFeatures.dlna.org"] = []string{contentFeatures}
		}

		// Since we're dealing with an io.Reader we can't
		// allow any HEAD requests that some DMRs trigger.
		if transcode && r.Method == http.MethodGet {
			_ = utils.ServeTranscodedStream(w, r, f.file, ff)
			return
		}

		name := strings.TrimLeft(r.URL.Path, "/")

		if r.Method == http.MethodGet {
			http.ServeContent(w, r, name, f.time, f.file)
		}

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
			contentFeatures, err := utils.BuildContentFeatures(mediaType, "00", transcode)
			if err != nil {
				http.NotFound(w, r)
				return
			}

			respHeader["contentFeatures.dlna.org"] = []string{contentFeatures}
		}

		// No seek support
		if r.Method == http.MethodGet {
			_, _ = io.Copy(w, f)
			f.Close()
		}
	default:
		http.NotFound(w, r)
		return
	}
}
