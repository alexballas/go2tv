package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/alexballas/go2tv/internal/gui"
	"github.com/alexballas/go2tv/internal/httphandlers"
	"github.com/alexballas/go2tv/internal/interactive"
	"github.com/alexballas/go2tv/internal/iptools"
	"github.com/alexballas/go2tv/internal/soapcalls"
)

var (
	version       string
	build         string
	dmrURL        string
	serverStarted = make(chan struct{})
	videoArg      = flag.String("v", "", "Path to the video file. (only works in interactive mode)")
	subsArg       = flag.String("s", "", "Path to the subtitles file. (only works in interactive mode) ")
	listPtr       = flag.Bool("l", false, "List all available UPnP/DLNA MediaRenderer models and URLs.")
	targetPtr     = flag.String("t", "", "Cast to a specific UPnP/DLNA MediaRenderer URL. (only works in interactive mode)")
	versionPtr    = flag.Bool("version", false, "Print version.")
)

func main() {
	guiEnabled := true

	flag.Parse()

	exit, err := checkflags()
	check(err)
	if exit {
		os.Exit(0)
	}
	if *videoArg != "" {
		guiEnabled = false
	}

	if guiEnabled {
		scr := gui.InitFyneNewScreen()
		gui.Start(scr)
	}

	absVideoFile, err := filepath.Abs(*videoArg)
	check(err)

	absSubtitlesFile, err := filepath.Abs(*subsArg)
	check(err)

	transportURL, controlURL, err := soapcalls.DMRextractor(dmrURL)
	check(err)

	whereToListen, err := iptools.URLtoListenIPandPort(dmrURL)
	check(err)

	scr, err := interactive.InitTcellNewScreen()

	check(err)

	// The String() method of the net/url package will properly escape the URL
	// compared to the url.QueryEscape() method.
	videoFileURLencoded := &url.URL{Path: filepath.Base(absVideoFile)}
	subsFileURLencoded := &url.URL{Path: filepath.Base(absSubtitlesFile)}

	tvdata := &soapcalls.TVPayload{
		TransportURL: transportURL,
		ControlURL:   controlURL,
		CallbackURL:  "http://" + whereToListen + "/callback",
		VideoURL:     "http://" + whereToListen + "/" + videoFileURLencoded.String(),
		SubtitlesURL: "http://" + whereToListen + "/" + subsFileURLencoded.String(),
	}

	s := httphandlers.NewServer(whereToListen)

	// We pass the tvdata here as we need the callback handlers to be able to react
	// to the different media renderer states.
	go func() {
		s.ServeFiles(serverStarted, absVideoFile, absSubtitlesFile, &httphandlers.HTTPPayload{Soapcalls: tvdata, Screen: scr})
	}()
	// Wait for HTTP server to properly initialize
	<-serverStarted

	err = tvdata.SendtoTV("Play1")
	check(err)

	scr.InterInit(*tvdata)
}

func check(err error) {
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}
}
