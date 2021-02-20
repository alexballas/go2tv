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

	"github.com/alexballas/go2tv/soapcalls"
)

// filesToServe defines the files we need to serve.
type filesToServe struct {
	Video     string
	Subtitles string
}

// HTTPserver - new http.Server instance.
type HTTPserver struct {
	http *http.Server
	mux  *http.ServeMux
}

// TVPayload - We need some of the soapcalls magic in
// this package too. We need to expose the ControlURL
// to the callback handler.
type TVPayload struct {
	Soapcalls soapcalls.TVPayload
}

// ServeFiles - Start HTTP server and serve the files.
func (s *HTTPserver) ServeFiles(serverStarted chan<- struct{}, videoPath, subtitlesPath string, tvpayload *TVPayload) {

	files := &filesToServe{
		Video:     videoPath,
		Subtitles: subtitlesPath,
	}

	s.mux.HandleFunc("/"+filepath.Base(files.Video), files.serveVideoHandler)
	s.mux.HandleFunc("/"+filepath.Base(files.Subtitles), files.serveSubtitlesHandler)
	s.mux.HandleFunc("/callback", tvpayload.callbackHandler)

	ln, err := net.Listen("tcp", s.http.Addr)
	check(err)

	serverStarted <- struct{}{}
	fmt.Println("Listening on:", s.http.Addr)
	s.http.Serve(ln)

}

// StopServeFiles - Kill the HTTP server.
func (s *HTTPserver) StopServeFiles() {
	s.http.Close()
}

func (f *filesToServe) serveVideoHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=017000 00000000000000000000000000")

	filePath, err := os.Open(f.Video)
	defer filePath.Close()
	check(err)

	fileStat, err := filePath.Stat()
	check(err)

	http.ServeContent(w, req, filepath.Base(f.Video), fileStat.ModTime(), filePath)
}

func (f *filesToServe) serveSubtitlesHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=017000 00000000000000000000000000")

	filePath, err := os.Open(f.Subtitles)
	defer filePath.Close()
	if err != nil {
		http.Error(w, "", 404)
		return
	}

	fileStat, err := filePath.Stat()
	if err != nil {
		http.Error(w, "", 404)
		return
	}
	http.ServeContent(w, req, filepath.Base(f.Subtitles), fileStat.ModTime(), filePath)
}

func (p *TVPayload) callbackHandler(w http.ResponseWriter, req *http.Request) {
	reqParsed, _ := io.ReadAll(req.Body)
	sidVal, sidExists := req.Header["Sid"]

	if !sidExists {
		http.Error(w, "", 404)
		return
	}

	if sidVal[0] == "" {
		http.Error(w, "", 404)
		return
	}

	uuid := sidVal[0]
	uuid = strings.TrimLeft(uuid, "[")
	uuid = strings.TrimLeft(uuid, "]")
	uuid = strings.TrimLeft(uuid, "uuid:")

	// Apparently we should ignore the first message
	// On some media renderers we receive a STOPPED message
	// even before we start streaming.
	seq, err := soapcalls.GetSequence(uuid)
	if err != nil {
		http.Error(w, "", 404)
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
		http.Error(w, "", 404)
		return
	}
	if !soapcalls.UpdateMRstate(previousstate, newstate, uuid) {
		http.Error(w, "", 404)
		return
	}
	if newstate == "PLAYING" {
		fmt.Println("Received: Playing")
	}
	if newstate == "PAUSED_PLAYBACK" {
		fmt.Println("Received: Paused")
	}
	if newstate == "STOPPED" {
		fmt.Println("Received: Stopped")
		p.Soapcalls.UnsubscribeSoapCall(uuid)
		os.Exit(0)
	}
	// TODO - Properly reply to that.
	fmt.Fprintf(w, "OK\n")
	return
}

// NewServer - create a new HTTP server.
func NewServer(a string) HTTPserver {
	srv := HTTPserver{
		http: &http.Server{Addr: a},
		mux:  http.NewServeMux(),
	}
	srv.http.Handler = srv.mux
	return srv
}

func check(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}
}
