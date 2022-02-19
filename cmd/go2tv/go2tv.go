package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/alexballas/go2tv/devices"
	"github.com/alexballas/go2tv/gui"
	"github.com/alexballas/go2tv/httphandlers"
	"github.com/alexballas/go2tv/interactive"
	soapcalls2 "github.com/alexballas/go2tv/soapcalls"
	"github.com/alexballas/go2tv/urlstreamer"
	utils2 "github.com/alexballas/go2tv/utils"
	"github.com/pkg/errors"
)

var (
	//go:embed version.txt
	version    string
	mediaArg   = flag.String("v", "", "Local path to the video/audio file. (Triggers the CLI mode)")
	urlArg     = flag.String("u", "", "HTTP URL to the media file. URL streaming does not support seek operations. (Triggers the CLI mode)")
	subsArg    = flag.String("s", "", "Local path to the subtitles file.")
	listPtr    = flag.Bool("l", false, "List all available UPnP/DLNA Media Renderer models and URLs.")
	targetPtr  = flag.String("t", "", "Cast to a specific UPnP/DLNA Media Renderer URL.")
	versionPtr = flag.Bool("version", false, "Print version.")
)

type flagResults struct {
	dmrURL string
	exit   bool
}

func main() {
	guiEnabled := true
	var mediaFile interface{}
	flag.Parse()

	flagRes, err := processflags()
	check(err)

	if flagRes.exit {
		os.Exit(0)
	}

	if len(os.Args) > 1 {
		guiEnabled = false
	}

	if *mediaArg != "" {
		mediaFile = *mediaArg
	}

	if *mediaArg == "" && *urlArg != "" {
		mediaFile, err = urlstreamer.StreamURL(context.Background(), *urlArg)
		check(err)
	}

	if guiEnabled {
		scr := gui.InitFyneNewScreen(version)
		gui.Start(scr)
	}

	var absMediaFile string
	var mediaType string

	switch t := mediaFile.(type) {
	case string:
		absMediaFile, err = filepath.Abs(t)
		check(err)
		mediaFile = absMediaFile

		mediaType, err = utils2.GetMimeDetailsFromFile(absMediaFile)
		check(err)
	case io.ReadCloser:
		absMediaFile = *urlArg
	}

	absSubtitlesFile, err := filepath.Abs(*subsArg)
	check(err)

	upnpServicesURLs, err := soapcalls2.DMRextractor(flagRes.dmrURL)
	check(err)

	whereToListen, err := utils2.URLtoListenIPandPort(flagRes.dmrURL)
	check(err)

	scr, err := interactive.InitTcellNewScreen()
	check(err)

	callbackPath, err := utils2.RandomString()
	check(err)

	tvdata := &soapcalls2.TVPayload{
		ControlURL:          upnpServicesURLs.AvtransportControlURL,
		EventURL:            upnpServicesURLs.AvtransportEventSubURL,
		RenderingControlURL: upnpServicesURLs.RenderingControlURL,
		CallbackURL:         "http://" + whereToListen + "/" + callbackPath,
		MediaURL:            "http://" + whereToListen + "/" + utils2.ConvertFilename(absMediaFile),
		SubtitlesURL:        "http://" + whereToListen + "/" + utils2.ConvertFilename(absSubtitlesFile),
		MediaType:           mediaType,
		CurrentTimers:       make(map[string]*time.Timer),
	}

	s := httphandlers.NewServer(whereToListen)
	serverStarted := make(chan struct{})

	// We pass the tvdata here as we need the callback handlers to be able to react
	// to the different media renderer states.
	go func() {
		err := s.ServeFiles(serverStarted, mediaFile, absSubtitlesFile, tvdata, scr)
		check(err)
	}()
	// Wait for HTTP server to properly initialize
	<-serverStarted

	scr.InterInit(tvdata)
}

func check(err error) {
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}
}

func listFlagFunction() error {
	flagsEnabled := 0
	flag.Visit(func(f *flag.Flag) {
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

	res := &flagResults{
		exit:   false,
		dmrURL: "",
	}

	if checkGUI() {
		return res, nil
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

	// The checkVflag should happen before
	// checkSflag so we're safe to call *mediaArg
	// here. If *subsArg is empty, try to
	// automatically find the srt from the
	// media file filename.
	*subsArg = (*mediaArg)[0:len(*mediaArg)-
		len(filepath.Ext(*mediaArg))] + ".srt"

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

func checkGUI() bool {
	return *mediaArg == "" && !*listPtr && *urlArg == ""
}
