package servfiles

import (
	"log"
	"net"
	"net/http"
	"os"
	"path"
)

// filesToServe defines the files we need to serve
type filesToServe struct {
	Video     string
	Subtitles string
}

// HTTPserverAndmux - combine the http and mux into on type
type HTTPserverAndmux struct {
	http *http.Server
	mux  *http.ServeMux
}

// ServeFiles - Start HTTP server and serve file
func (s *HTTPserverAndmux) ServeFiles(serverStarted chan<- struct{}, videoPath, subtitlesPath string) {
	files := &filesToServe{
		Video:     videoPath,
		Subtitles: subtitlesPath,
	}

	s.mux.HandleFunc("/"+path.Base(files.Video), files.serveVideoHandler)

	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		log.Fatal(err)
	}
	serverStarted <- struct{}{}
	log.Println("Listening on :3000...")
	s.http.Serve(ln)

}

// StopServeFiles - Kill the HTTP server
func (s *HTTPserverAndmux) StopServeFiles() {
	s.http.Close()
}

func (f *filesToServe) serveVideoHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=017000 00000000000000000000000000")

	filePath, err := os.Open(f.Video)
	if err != nil {
		log.Fatal(err)
	}

	fileStat, err := filePath.Stat()
	if err != nil {
		log.Fatal(err)
	}

	http.ServeContent(w, req, path.Base(f.Video), fileStat.ModTime(), filePath)

}

// NewServer - create a new HTTP server
func NewServer(adr string) HTTPserverAndmux {
	return HTTPserverAndmux{
		http: &http.Server{Addr: ":3000"},
		mux:  http.NewServeMux(),
	}

}
