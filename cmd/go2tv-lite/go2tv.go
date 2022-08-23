package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alexballas/go2tv/devices"
	"github.com/alexballas/go2tv/httphandlers"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/alexballas/go2tv/utils"
)

var (
	//go:embed version.txt
	version      string
	errNoflag    = errors.New("no flag used")
	mediaArg     = flag.String("v", "", "Local path to the video/audio file. (Triggers the CLI mode)")
	urlArg       = flag.String("u", "", "HTTP URL to the media file. URL streaming does not support seek operations. (Triggers the CLI mode)")
	subsArg      = flag.String("s", "", "Local path to the subtitles file.")
	targetPtr    = flag.String("t", "", "Cast to a specific UPnP/DLNA Media Renderer URL.")
	transcodePtr = flag.Bool("tc", false, "Use ffmpeg to transcode input video file.")
	listPtr      = flag.Bool("l", false, "List all available UPnP/DLNA Media Renderer models and URLs.")
	versionPtr   = flag.Bool("version", false, "Print version.")
)

type dummyScreen struct{}

type flagResults struct {
	dmrURL string
	exit   bool
}

func main() {
	var absMediaFile string
	var mediaType string
	var mediaFile interface{}
	var isSeek bool
	var tvdata *soapcalls.TVPayload
	var s *httphandlers.HTTPserver

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	go func() {
		for range c {
			if tvdata != nil {
				_ = tvdata.SendtoTV("Stop")
			}
			if s != nil {
				s.StopServer()
			}
			fmt.Println("force exiting..")
			os.Exit(0)
		}
	}()

	flag.Parse()

	flagRes, err := processflags()
	check(err)

	if flagRes.exit {
		os.Exit(0)
	}

	if *mediaArg != "" {
		mediaFile = *mediaArg
	}

	if *mediaArg == "" && *urlArg != "" {
		mediaURL, err := utils.StreamURL(context.Background(), *urlArg)
		check(err)

		mediaURLinfo, err := utils.StreamURL(context.Background(), *urlArg)
		check(err)

		mediaType, err = utils.GetMimeDetailsFromStream(mediaURLinfo)
		check(err)

		mediaFile = mediaURL

		if strings.Contains(mediaType, "image") {
			readerToBytes, err := io.ReadAll(mediaURL)
			mediaURL.Close()
			check(err)
			mediaFile = readerToBytes
		}
	}

	switch t := mediaFile.(type) {
	case string:
		absMediaFile, err = filepath.Abs(t)
		check(err)

		mfile, err := os.Open(absMediaFile)
		check(err)

		mediaFile = absMediaFile
		mediaType, err = utils.GetMimeDetailsFromFile(mfile)

		if !*transcodePtr {
			isSeek = true
		}

		check(err)
	case io.ReadCloser, []byte:
		absMediaFile = *urlArg
	}

	absSubtitlesFile, err := filepath.Abs(*subsArg)
	check(err)

	upnpServicesURLs, err := soapcalls.DMRextractor(flagRes.dmrURL)
	check(err)

	whereToListen, err := utils.URLtoListenIPandPort(flagRes.dmrURL)
	check(err)

	scr := &dummyScreen{}

	callbackPath, err := utils.RandomString()
	check(err)

	tvdata = &soapcalls.TVPayload{
		ControlURL:                  upnpServicesURLs.AvtransportControlURL,
		EventURL:                    upnpServicesURLs.AvtransportEventSubURL,
		RenderingControlURL:         upnpServicesURLs.RenderingControlURL,
		CallbackURL:                 "http://" + whereToListen + "/" + callbackPath,
		MediaURL:                    "http://" + whereToListen + "/" + utils.ConvertFilename(absMediaFile),
		SubtitlesURL:                "http://" + whereToListen + "/" + utils.ConvertFilename(absSubtitlesFile),
		MediaType:                   mediaType,
		CurrentTimers:               make(map[string]*time.Timer),
		MediaRenderersStates:        make(map[string]*soapcalls.States),
		InitialMediaRenderersStates: make(map[string]bool),
		RWMutex:                     &sync.RWMutex{},
		Transcode:                   *transcodePtr,
		Seekable:                    isSeek,
		Logging:                     nil,
	}

	s = httphandlers.NewServer(whereToListen)
	serverStarted := make(chan struct{})

	// We pass the tvdata here as we need the callback handlers to be able to react
	// to the different media renderer states.
	go func() {
		err := s.StartServer(serverStarted, mediaFile, absSubtitlesFile, tvdata, scr)
		check(err)
	}()
	// Wait for HTTP server to properly initialize
	<-serverStarted

	err = tvdata.SendtoTV("Play1")
	check(err)

	select {}
}

func check(err error) {
	if errors.Is(err, errNoflag) {
		flag.Usage()
		os.Exit(0)
	}

	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}
}

func listFlagFunction() error {
	flagsEnabled := 0
	flag.Visit(func(*flag.Flag) {
		flagsEnabled++
	})

	if flagsEnabled > 1 {
		return errors.New("cant combine -l with other flags")
	}

	deviceList, err := devices.LoadSSDPservices(1)
	if err != nil {
		return errors.New("failed to list devices")
	}

	fmt.Println()

	// We loop through this map twice as we need to maintain
	// the correct order.
	keys := make([]string, 0)
	for k := range deviceList {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for q, k := range keys {
		boldStart := ""
		boldEnd := ""

		if runtime.GOOS == "linux" {
			boldStart = "\033[1m"
			boldEnd = "\033[0m"
		}
		fmt.Printf("%sDevice %v%s\n", boldStart, q+1, boldEnd)
		fmt.Printf("%s--------%s\n", boldStart, boldEnd)
		fmt.Printf("%sModel:%s %s\n", boldStart, boldEnd, k)
		fmt.Printf("%sURL:%s   %s\n", boldStart, boldEnd, deviceList[k])
		fmt.Println()
	}

	return nil
}

func processflags() (*flagResults, error) {
	checkVerflag()

	res := &flagResults{}

	if *mediaArg == "" && !*listPtr && *urlArg == "" {
		return nil, fmt.Errorf("checkflags error: %w", errNoflag)
	}

	if err := checkTCflag(); err != nil {
		return nil, fmt.Errorf("checkflags error: %w", err)
	}

	if err := checkTflag(res); err != nil {
		return nil, fmt.Errorf("checkflags error: %w", err)
	}

	list, err := checkLflag()
	if err != nil {
		return nil, fmt.Errorf("checkflags error: %w", err)
	}

	if list {
		res.exit = true
		return res, nil
	}

	if err := checkVflag(); err != nil {
		return nil, fmt.Errorf("checkflags error: %w", err)
	}

	if err := checkSflag(); err != nil {
		return nil, fmt.Errorf("checkflags error: %w", err)
	}

	return res, nil
}

func checkVflag() error {
	if !*listPtr && *urlArg == "" {
		if _, err := os.Stat(*mediaArg); os.IsNotExist(err) {
			return fmt.Errorf("checkVflags error: %w", err)
		}
	}

	return nil
}

func checkSflag() error {
	if *subsArg != "" {
		if _, err := os.Stat(*subsArg); os.IsNotExist(err) {
			return fmt.Errorf("checkSflags error: %w", err)
		}
		return nil
	}

	// The checkVflag should happen before checkSflag so we're safe to call
	// *mediaArg here. If *subsArg is empty, try to automatically find the
	// srt from the media file filename.
	*subsArg = (*mediaArg)[0:len(*mediaArg)-
		len(filepath.Ext(*mediaArg))] + ".srt"

	return nil
}

func checkTCflag() error {
	if *transcodePtr {
		_, err := exec.LookPath("ffmpeg")
		if err != nil {
			return fmt.Errorf("checkTCflag parse error: %w", err)
		}
	}

	return nil
}

func checkTflag(res *flagResults) error {
	if *targetPtr != "" {
		// Validate URL before proceeding.
		_, err := url.ParseRequestURI(*targetPtr)
		if err != nil {
			return fmt.Errorf("checkTflag parse error: %w", err)
		}

		res.dmrURL = *targetPtr
		return nil
	}

	deviceList, err := devices.LoadSSDPservices(1)
	if err != nil {
		return fmt.Errorf("checkTflag service loading error: %w", err)
	}

	res.dmrURL, err = devices.DevicePicker(deviceList, 1)
	if err != nil {
		return fmt.Errorf("checkTflag device picker error: %w", err)
	}

	return nil
}

func checkLflag() (bool, error) {
	if *listPtr {
		if err := listFlagFunction(); err != nil {
			return false, fmt.Errorf("checkLflag error: %w", err)
		}
		return true, nil
	}

	return false, nil
}

func checkVerflag() {
	if *versionPtr && os.Args[1] == "-version" {
		fmt.Printf("Go2TV Version: %s\n", version)
		os.Exit(0)
	}
}

func (s *dummyScreen) EmitMsg(msg string) {
	fmt.Println(msg)
}

func (s *dummyScreen) Fini() {
	fmt.Println("exiting..")
	os.Exit(0)
}
