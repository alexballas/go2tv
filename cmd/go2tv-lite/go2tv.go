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
	"syscall"

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

type dummyScreen struct {
	ctxCancel context.CancelFunc
}

type flagResults struct {
	dmrURL string
	exit   bool
}

func main() {
	if err := run(); err != nil {
		if errors.Is(err, errNoflag) {
			flag.Usage()
			os.Exit(0)
		}

		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
			os.Exit(1)
		}
	}
}

func run() error {
	var absMediaFile string
	var mediaType string
	var mediaFile interface{}
	var isSeek bool
	var s *httphandlers.HTTPserver

	keyboardExitCTX, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	exitCallbackCTX, cancel2 := context.WithCancel(keyboardExitCTX)
	defer cancel2()
	/*
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
	*/
	flag.Parse()

	flagRes, err := processflags()
	if err != nil {
		return err
	}

	if flagRes.exit {
		return nil
	}

	if *mediaArg != "" {
		mediaFile = *mediaArg
	}

	if *mediaArg == "" && *urlArg != "" {
		mediaURL, err := utils.StreamURL(context.Background(), *urlArg)
		if err != nil {
			return err
		}

		mediaURLinfo, err := utils.StreamURL(context.Background(), *urlArg)
		if err != nil {
			return err
		}

		mediaType, err = utils.GetMimeDetailsFromStream(mediaURLinfo)
		if err != nil {
			return err
		}

		mediaFile = mediaURL

		if strings.Contains(mediaType, "image") {
			readerToBytes, err := io.ReadAll(mediaURL)
			mediaURL.Close()
			if err != nil {
				return err
			}
			mediaFile = readerToBytes
		}
	}

	switch t := mediaFile.(type) {
	case string:
		absMediaFile, err = filepath.Abs(t)
		if err != nil {
			return err
		}

		mfile, err := os.Open(absMediaFile)
		if err != nil {
			return err
		}

		mediaFile = absMediaFile
		mediaType, err = utils.GetMimeDetailsFromFile(mfile)
		if err != nil {
			return err
		}

		if !*transcodePtr {
			isSeek = true
		}
	case io.ReadCloser, []byte:
		absMediaFile = *urlArg
	}

	absSubtitlesFile, err := filepath.Abs(*subsArg)
	if err != nil {
		return err
	}

	scr := &dummyScreen{ctxCancel: cancel2}

	tvdata, err := soapcalls.NewTVPayload(soapcalls.Options{
		DMR:       flagRes.dmrURL,
		Media:     absMediaFile,
		Subs:      absSubtitlesFile,
		Mtype:     mediaType,
		Transcode: *transcodePtr,
		Seek:      isSeek,
		Logging:   os.Stdout,
	})
	if err != nil {
		return err
	}

	s = httphandlers.NewServer(tvdata.ListenAddress())
	serverStarted := make(chan error)

	// We pass the tvdata here as we need the callback handlers to be able to react
	// to the different media renderer states.
	go func() {
		s.StartServer(serverStarted, mediaFile, absSubtitlesFile, tvdata, scr)
	}()
	// Wait for HTTP server to properly initialize
	if err := <-serverStarted; err != nil {
		return err
	}

	err = tvdata.SendtoTV("Play1")
	if err != nil {
		return err
	}

	<-exitCallbackCTX.Done()

	if tvdata != nil {
		_ = tvdata.SendtoTV("Stop")
	}
	if s != nil {
		s.StopServer()
	}

	return nil
}

func listFlagFunction() error {
	flagsEnabled := 0
	flag.Visit(func(*flag.Flag) {
		flagsEnabled++
	})

	if flagsEnabled > 1 {
		return errors.New("can't combine -l with other flags")
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
	res := &flagResults{}

	if checkVerflag() {
		res.exit = true
		return res, nil
	}

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

func checkVerflag() bool {
	if *versionPtr && os.Args[1] == "-version" {
		fmt.Printf("Go2TV Version: %s\n", version)
		return true
	}
	return false
}

func (s *dummyScreen) EmitMsg(msg string) {
	fmt.Println(msg)
}

func (s *dummyScreen) Fini() {
	fmt.Println("exiting..")
	s.ctxCancel()
}
