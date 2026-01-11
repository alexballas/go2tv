//go:build !android

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
	"strings"
	"syscall"
	"time"

	"github.com/alexballas/go2tv/devices"
	"github.com/alexballas/go2tv/httphandlers"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/alexballas/go2tv/utils"
	tea "github.com/charmbracelet/bubbletea"
)

var (
	version      = "dev"
	errNoflag    = errors.New("no flag used")
	mediaArg     = flag.String("v", "", "Local path to the video/audio file. (Triggers the CLI mode)")
	urlArg       = flag.String("u", "", "HTTP URL to the media file. URL streaming does not support seek operations. (Triggers the CLI mode)")
	subsArg      = flag.String("s", "", "Local path to the subtitles file.")
	targetPtr    = flag.String("t", "", "Cast to a specific UPnP/DLNA Media Renderer URL.")
	transcodePtr = flag.Bool("tc", false, "Use ffmpeg to transcode input video file.")
	listPtr      = flag.Bool("l", false, "List all available UPnP/DLNA Media Renderer models and URLs.")
	versionPtr   = flag.Bool("version", false, "Print version.")

	errNoCombi    = errors.New("can't combine -l with other flags")
	errFailtoList = errors.New("failed to list devices")
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

		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		absMediaFile, mediaType string
		mediaFile               any
		isSeek                  bool
		s                       *httphandlers.HTTPserver
	)

	exitCTX, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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

	scr := &dummyScreen{ctxCancel: cancel}

	tvdata, err := soapcalls.NewTVPayload(&soapcalls.Options{
		DMR:       flagRes.dmrURL,
		Media:     absMediaFile,
		Subs:      absSubtitlesFile,
		Mtype:     mediaType,
		Transcode: *transcodePtr,
		Seek:      isSeek,
		LogOutput: nil,
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

	<-exitCTX.Done()

	if tvdata != nil {
		_ = tvdata.SendtoTV("Stop")
	}
	if s != nil {
		s.StopServer()
	}

	return nil
}

type Device struct {
	Model string
	URL   string
}

type refreshMsg []Device

type listDevicesModel struct {
	devices []Device
}

func checkDevices() tea.Cmd {
	return func() tea.Msg {
		deviceList, _ := devices.LoadAllDevices(2)

		var rMsg refreshMsg
		for _, dev := range deviceList {
			rMsg = append(rMsg, Device{
				Model: dev.Name,
				URL:   dev.Addr,
			})
		}

		return rMsg
	}
}

func (m listDevicesModel) Init() tea.Cmd {
	devices.StartChromecastDiscoveryLoop(context.Background())
	return checkDevices()
}

func (m listDevicesModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" {
			return m, tea.Quit
		}

	case refreshMsg:
		m.devices = msg
		return m, tea.Tick(time.Second*2, func(t time.Time) tea.Msg {
			return checkDevices()()
		})
	}

	return m, nil
}

func (m listDevicesModel) View() string {
	s := "Scanning devices... (q to quit)\n\n"
	for _, dev := range m.devices {
		s += "• " + dev.Model + " [" + dev.URL + "] " + "\n"
	}
	return s
}

func listFlagFunction() error {
	flagsEnabled := 0
	flag.Visit(func(*flag.Flag) {
		flagsEnabled++
	})

	if flagsEnabled > 1 {
		return errNoCombi
	}

	p := tea.NewProgram(listDevicesModel{})
	_, err := p.Run()

	return err
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

		if *targetPtr == "" {
			return fmt.Errorf("checkVflags error: %w", errors.New("no target device specified with -t flag"))
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
