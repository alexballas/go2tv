package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/alexballas/go2tv/iptools"
	"github.com/alexballas/go2tv/servfiles"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/koron/go-ssdp"
)

var serverStarted = make(chan struct{})
var devices = make(map[int][]string)

var videoArg = flag.String("video", "/home/alex/VIDEO0170.mp4", "Path to the video file")
var subsArg = flag.String("sub", "", "Path to the subtitles file")
var listPtr = flag.Bool("list", false, "List available UPnP/DLNA MediaRenderers")
var targetPtr = flag.String("target", "", "Cast to a specific UPnP/DLNA MediaRenderer URL")

func main() {
	flag.Parse()

	if *targetPtr == "" {
		if err := loadSSDPservices(); err != nil {
			log.Fatal(err)
		}
	}

	if *listPtr == true {
		if len(devices) == 0 {
			log.Fatal("-list and -target can't be used together")
		}
	}

	if *videoArg == "" {
		log.Fatal("No video file defined")
	}
	if _, err := os.Stat(*videoArg); os.IsNotExist(err) {
		log.Fatalf("Could not locate video file %v", *videoArg)
	}

	absVideoFile, err := filepath.Abs(*videoArg)
	if err != nil {
		log.Fatalf("Could not retriece absolute path for %v", *videoArg)
	}

	absSubtitlesFile := *subsArg

	tvdata := &soapcalls.TVPayload{
		TransportURL: "http://192.168.88.244:9197/upnp/control/AVTransport1",
		VideoURL:     "http://192.168.88.250:3000/VIDEO0170.mp4",
		SubtitlesURL: "http://192.168.88.250:3000/VIDEO0170.srt",
	}
	whereToListen, err := iptools.URLtoListeIP("http://192.168.88.244:9197/drm")
	if err != nil {
		log.Fatal(err)
	}

	s := servfiles.NewServer(whereToListen + ":3000")
	go func() { s.ServeFiles(serverStarted, absVideoFile, absSubtitlesFile) }()
	calctime := time.Now()
	// Wait for HTTP server to properly initialize
	<-serverStarted

	fmt.Println(time.Since(calctime))
	if err := tvdata.SendtoTV("Play"); err != nil {
		log.Fatal(err)
	}
	time.Sleep(10 * time.Second)
	if err := tvdata.SendtoTV("Stop"); err != nil {
		log.Fatal(err)
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
			srvLocation, err := iptools.URLtoIP(srv.Location)
			if err != nil {
				return err
			}
			devices[counter] = []string{srv.Server, srvLocation}
		}
	}
	if counter > 0 {
		return nil
	}
	return errors.New("No available Media Renderers")
}
