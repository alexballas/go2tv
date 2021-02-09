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

// HTTPserver - new http.Server instance
type HTTPserver struct {
	http *http.Server
}

// ServeFiles - Start HTTP server and serve file
func (s *HTTPserver) ServeFiles(serverStarted chan<- struct{}, videoPath, subtitlesPath string) {
	files := &filesToServe{
		Video:     videoPath,
		Subtitles: subtitlesPath,
	}

	http.HandleFunc("/"+path.Base(files.Video), files.serveVideoHandler)

	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		log.Fatal(err)
	}
	serverStarted <- struct{}{}
	log.Println("Listening on:", s.http.Addr)
	s.http.Serve(ln)

}

// StopServeFiles - Kill the HTTP server
func (s *HTTPserver) StopServeFiles() {
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
func NewServer(a string) HTTPserver {
	return HTTPserver{
		http: &http.Server{Addr: a},
	}

}
