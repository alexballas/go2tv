package servefiles

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
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

	http.HandleFunc("/"+filepath.Base(files.Video), files.serveVideoHandler)
	http.HandleFunc("/"+filepath.Base(files.Subtitles), files.serveSubtitlesHandler)

	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}
	serverStarted <- struct{}{}
	fmt.Println("Listening on:", s.http.Addr)
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
	defer filePath.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

	fileStat, err := filePath.Stat()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}
	http.ServeContent(w, req, filepath.Base(f.Video), fileStat.ModTime(), filePath)

}

func (f *filesToServe) serveSubtitlesHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=017000 00000000000000000000000000")

	filePath, err := os.Open(f.Subtitles)
	defer filePath.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

	fileStat, err := filePath.Stat()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}
	http.ServeContent(w, req, filepath.Base(f.Subtitles), fileStat.ModTime(), filePath)

}

// NewServer - create a new HTTP server
func NewServer(a string) HTTPserver {
	return HTTPserver{
		http: &http.Server{Addr: a},
	}
}
