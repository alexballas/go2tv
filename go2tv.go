package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/alexballas/go2tv/servfiles"
	"github.com/alexballas/go2tv/soapcalls"
)

var serverStarted = make(chan struct{})
var videoArg = flag.String("video", "", "Path of our video file")
var subsArg = flag.String("sub", "", "Path of our subs file (optional)")

func main() {
	/*
		list, err := ssdp.Search(ssdp.All, 1, "")
		if err != nil {
			log.Fatal(err)
		}
		for i, srv := range list {
			// We only care about the AVTransport
			if srv.Type == "urn:schemas-upnp-org:service:AVTransport:1" {
				fmt.Printf("%d: %s %s %s\n", i, srv.Type, srv.Location, srv.Server)
			}

		}
	*/
	flag.Parse()

	if *videoArg == "" {
		log.Fatalf("No video file defined")
	}
	if _, err := os.Stat(*videoArg); os.IsNotExist(err) {
		log.Fatalf("Could not locate video file %v", *videoArg)
	}

	absVideoFile, err := filepath.Abs(*videoArg)
	absSubtitlesFile := *subsArg

	if err != nil {
		log.Fatalf("Could not retriece absolute path for %v", *videoArg)
	}

	tvdata := &soapcalls.TVPayload{
		TransportURL: "http://192.168.88.244:9197/upnp/control/AVTransport1",
		VideoURL:     "http://192.168.88.250:3000/VIDEO0170.mp4",
		SubtitlesURL: "http://192.168.88.250:3000/VIDEO0170.srt",
	}
	go func() { servfiles.ServeFiles(serverStarted, absVideoFile, absSubtitlesFile) }()
	calctime := time.Now()
	// Wait for HTTP server to properly initialize
	<-serverStarted
	fmt.Println(time.Since(calctime))
	tvdata.SendtoTV("Play")
	time.Sleep(10 * time.Second)
	tvdata.SendtoTV("Stop")

	servfiles.StopServeFiles()
	select {}
}
