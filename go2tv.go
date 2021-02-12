package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/alexballas/go2tv/iptools"
	"github.com/alexballas/go2tv/servefiles"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/koron/go-ssdp"
)

var (
	dmrURL        string
	serverStarted = make(chan struct{})
	devices       = make(map[int][]string)
	videoArg      = flag.String("v", "", "Path to the video file.")
	subsArg       = flag.String("s", "", "Path to the subtitles file.")
	listPtr       = flag.Bool("l", false, "List all available UPnP/DLNA MediaRenderer models and URLs.")
	targetPtr     = flag.String("t", "", "Cast to a specific UPnP/DLNA MediaRenderer URL.")
)

func main() {
	flag.Parse()

	exit, err := checkflags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

	if exit == true {
		os.Exit(0)
	}

	absVideoFile, err := filepath.Abs(*videoArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

	absSubtitlesFile, err := filepath.Abs(*subsArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

	transportURL, err := soapcalls.AVTransportFromDMRextractor(dmrURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

	whereToListen, err := iptools.URLtoListenIPandPort(dmrURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

	tvdata := &soapcalls.TVPayload{
		TransportURL: transportURL,
		VideoURL:     "http://" + whereToListen + "/" + filepath.Base(absVideoFile),
		SubtitlesURL: "http://" + whereToListen + "/" + filepath.Base(absSubtitlesFile),
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

	s := servefiles.NewServer(whereToListen)
	go func() { s.ServeFiles(serverStarted, absVideoFile, absSubtitlesFile) }()
	// Wait for HTTP server to properly initialize
	<-serverStarted
	if err := tvdata.SendtoTV("Play"); err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

	initializeCloseHandler(*tvdata)
	// Sleep forever
	select {}
}

func loadSSDPservices() error {
	list, err := ssdp.Search(ssdp.All, 1, "")
	if err != nil {
		return err
	}
	counter := 0
	for _, srv := range list {
		// We only care about the AVTransport services
		if srv.Type == "urn:schemas-upnp-org:service:AVTransport:1" {
			counter++
			devices[counter] = []string{srv.Server, srv.Location}
		}
	}
	if counter > 0 {
		return nil
	}
	return errors.New("loadSSDPservices: No available Media Renderers")
}

func devicePicker(i int) (string, error) {
	if i > len(devices) || len(devices) == 0 || i <= 0 {
		return "", errors.New("devicePicker: Requested device not available")
	}
	keys := make([]int, 0)
	for k := range devices {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		if i == k {
			return devices[k][1], nil
		}
	}
	return "", errors.New("devicePicker: Something went terribly wrong")
}

func initializeCloseHandler(tvdata soapcalls.TVPayload) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Println(" Shutting down...")
		if err := tvdata.SendtoTV("Stop"); err != nil {
			fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
}
