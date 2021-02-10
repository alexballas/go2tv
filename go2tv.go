package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/alexballas/go2tv/iptools"
	"github.com/alexballas/go2tv/servfiles"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/koron/go-ssdp"
)

var (
	serverStarted = make(chan struct{})
	devices       = make(map[int][]string)

	videoArg  = flag.String("v", "", "Path to the video file.")
	subsArg   = flag.String("s", "", "Path to the subtitles file.")
	listPtr   = flag.Bool("l", false, "List all available UPnP/DLNA MediaRenderer models and URLs.")
	targetPtr = flag.String("t", "", "Cast to a specific UPnP/DLNA MediaRenderer URL.")
)

func main() {
	var dmrURL string
	flag.Parse()

	if *targetPtr == "" {
		err := loadSSDPservices()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
			os.Exit(1)
		}

		dmrURL, err = devicePicker(1)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
			os.Exit(1)
		}
	} else {
		// Validate URL before proceeding
		_, err := url.ParseRequestURI(*targetPtr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
			os.Exit(1)
		}
		dmrURL = *targetPtr
	}

	if *listPtr == true {
		if len(devices) == 0 {
			err := errors.New("-l and -t can't be used together")
			fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
			os.Exit(1)
		}
		fmt.Println()

		// We loop through this map twice as we need to maintain
		// the correct order.
		keys := make([]int, 0)
		for k := range devices {
			keys = append(keys, k)
		}

		sort.Ints(keys)

		for _, k := range keys {
			fmt.Printf("\033[1mDevice %v\033[0m\n", k)
			fmt.Printf("\033[1m--------\033[0m\n")
			fmt.Printf("\033[1mModel\033[0m: %s\n", devices[k][0])
			fmt.Printf("\033[1mURL\033[0m:   %s\n", devices[k][1])
			fmt.Println()
		}
		os.Exit(0)
	}

	if *videoArg == "" {
		err := errors.New("No video file defined")
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}
	if _, err := os.Stat(*videoArg); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

	absVideoFile, err := filepath.Abs(*videoArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

	absSubtitlesFile := *subsArg

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

	s := servfiles.NewServer(whereToListen)
	go func() { s.ServeFiles(serverStarted, absVideoFile, absSubtitlesFile) }()
	// Wait for HTTP server to properly initialize
	<-serverStarted

	if err := tvdata.SendtoTV("Play"); err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

	// Just for debugging reasons
	time.Sleep(10 * time.Second)
	if err := tvdata.SendtoTV("Stop"); err != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}

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
