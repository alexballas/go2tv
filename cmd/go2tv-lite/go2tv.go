//go:build !android

package main

import (
	"bytes"
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

	tea "github.com/charmbracelet/bubbletea"
	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

var (
	version   = "dev"
	errNoflag = errors.New("no flag used")
	mediaArg  = flag.String("v", "", "Path to video/audio file (triggers CLI mode).")
	urlArg    = flag.String("u", "", "URL to media file (triggers CLI mode).")

	subsArg      = flag.String("s", "", "Path to subtitles file (.srt or .vtt).")
	targetPtr    = flag.String("t", "", "Device URL to cast to (from -l output).")
	transcodePtr = flag.Bool("tc", false, "Force transcoding with ffmpeg.")
	listPtr      = flag.Bool("l", false, "List available devices (Smart TVs and Chromecasts).")

	versionPtr = flag.Bool("version", false, "Print version.")

	errNoCombi = errors.New("can't combine -l with other flags")
)

type dummyScreen struct {
	ctxCancel context.CancelFunc
	Client    *castprotocol.CastClient
}

type flagResults struct {
	targetURL string

	exit bool
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

	if checkStdin() && *mediaArg == "" {
		*mediaArg = "-"
	}

	if *mediaArg == "-" {
		head := make([]byte, 512)
		n, err := io.ReadFull(os.Stdin, head)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return err
		}

		mediaType, err = utils.GetMimeDetailsFromBytes(head[:n])
		if err != nil {
			return err
		}

		mediaFile = struct {
			io.Reader
			io.Closer
		}{
			Reader: io.MultiReader(bytes.NewReader(head[:n]), os.Stdin),
			Closer: os.Stdin,
		}

		// Use a valid path-like identifier for URL construction
		absMediaFile = "stdin.stream"
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
		// Only set absMediaFile if not already set (stdin case already set it to "stdin.stream")
		if absMediaFile == "" {
			absMediaFile = *urlArg
		}
	}

	absSubtitlesFile, err := filepath.Abs(*subsArg)
	if err != nil {
		return err
	}

	// Get ffmpeg path for transcoding
	ffmpegPath, _ := exec.LookPath("ffmpeg")

	// Branch based on device type
	if devices.IsChromecastURL(flagRes.targetURL) {
		return runChromecastCLI(exitCTX, cancel, flagRes.targetURL, absMediaFile, mediaFile, mediaType, absSubtitlesFile, ffmpegPath, *transcodePtr)
	}

	scr := &dummyScreen{ctxCancel: cancel}

	tvdata, err := soapcalls.NewTVPayload(&soapcalls.Options{
		DMR:            flagRes.targetURL,
		Media:          absMediaFile,
		Subs:           absSubtitlesFile,
		Mtype:          mediaType,
		Transcode:      *transcodePtr,
		Seek:           isSeek,
		FFmpegPath:     ffmpegPath,
		FFmpegSubsPath: absSubtitlesFile,
		FFmpegSeek:     0,
		LogOutput:      nil,
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

func runChromecastCLI(ctx context.Context, cancel context.CancelFunc, deviceURL, mediaPath string, mediaFile any, mediaType, subsPath, ffmpegPath string, transcode bool) error {
	if !strings.Contains(mediaType, "video") {
		transcode = false
	}

	// Create Chromecast client
	client, err := castprotocol.NewCastClient(deviceURL)
	if err != nil {
		return fmt.Errorf("chromecast init: %w", err)
	}

	if err := client.Connect(); err != nil {
		return fmt.Errorf("chromecast connect: %w", err)
	}
	defer client.Close(true)

	// Get listen address from device URL
	whereToListen, err := utils.URLtoListenIPandPort(deviceURL)
	if err != nil {
		return fmt.Errorf("chromecast listen addr: %w", err)
	}

	// Start HTTP server for media
	httpServer := httphandlers.NewServer(whereToListen)
	serverStarted := make(chan error)

	// Create TranscodeOptions if transcoding enabled
	var tcOpts *utils.TranscodeOptions
	var mediaDuration float64
	if transcode {
		// Get actual media duration from ffprobe (Chromecast can't detect it for transcoded streams)
		// Only works for file paths, not streams
		if _, isStream := mediaFile.(io.ReadCloser); !isStream {
			if duration, err := utils.DurationForMediaSeconds(ffmpegPath, mediaPath); err == nil {
				mediaDuration = duration
			}
		}

		// Determine subtitle path for burning
		tcSubsPath := ""
		if subsPath != "" {
			if _, err := os.Stat(subsPath); err == nil {
				tcSubsPath = subsPath
			}
		}

		tcOpts = &utils.TranscodeOptions{
			FFmpegPath:   ffmpegPath,
			SubsPath:     tcSubsPath,
			SeekSeconds:  0,
			SubtitleSize: utils.SubtitleSizeMedium,
			LogOutput:    nil, // CLI uses stdout
		}
		// Update content type for transcoded output
		mediaType = "video/mp4"
	}

	// Build media URL
	mediaURL := "http://" + whereToListen + "/" + utils.ConvertFilename(mediaPath)

	// Handle streams (stdin) vs file paths differently
	if stream, isStream := mediaFile.(io.ReadCloser); isStream {
		// For streams: manually add handler then start serving
		mediaFilename := "/" + utils.ConvertFilename(mediaPath)
		httpServer.AddHandler(mediaFilename, nil, tcOpts, stream)

		go func() {
			httpServer.StartServing(serverStarted)
		}()
	} else {
		// For file paths: use the existing simple server with transcode
		go func() {
			httpServer.StartSimpleServerWithTranscode(serverStarted, mediaPath, tcOpts)
		}()
	}

	if err := <-serverStarted; err != nil {
		return fmt.Errorf("chromecast server: %w", err)
	}

	// Handle subtitles (WebVTT side-loading - only when NOT transcoding)
	subtitleURL := ""
	if subsPath != "" && !transcode {
		if _, err := os.Stat(subsPath); err == nil {
			ext := strings.ToLower(filepath.Ext(subsPath))
			switch ext {
			case ".srt":
				webvttData, err := utils.ConvertSRTtoWebVTT(subsPath)
				if err == nil {
					httpServer.AddHandler("/subtitles.vtt", nil, nil, webvttData)
					subtitleURL = "http://" + whereToListen + "/subtitles.vtt"
				}
			case ".vtt":
				httpServer.AddHandler("/subtitles.vtt", nil, nil, subsPath)
				subtitleURL = "http://" + whereToListen + "/subtitles.vtt"
			}
		}
	}

	// Init interactive screen (dummy for lite)
	scr := &dummyScreen{ctxCancel: cancel}
	scr.Client = client

	// Load media (async)
	go func() {
		// Use LIVE stream type for URL/stdin streams (DMR shows LIVE badge, but buffer unchanged)
		_, isStream := mediaFile.(io.ReadCloser)
		if err := client.Load(mediaURL, mediaType, 0, mediaDuration, subtitleURL, isStream); err != nil {
			fmt.Fprintf(os.Stderr, "chromecast load: %v\n", err)
		}
	}()

	// Wait for exit signal
	<-ctx.Done()

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
	var s strings.Builder
	s.WriteString("Scanning devices... (q to quit)\n\n")
	for _, dev := range m.devices {
		s.WriteString("• " + dev.Model + " [" + dev.URL + "] " + "\n")
	}
	return s.String()
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

	if *mediaArg == "" && !*listPtr && *urlArg == "" && !checkStdin() {
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
		if _, err := os.Stat(*mediaArg); os.IsNotExist(err) && !checkStdin() && *mediaArg != "-" {
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

		res.targetURL = *targetPtr
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
	if s.Client != nil {
		_ = s.Client.Stop()
	}
	fmt.Println("exiting..")
	s.ctxCancel()
}

func (s *dummyScreen) SetMediaType(mediaType string) {
	// No-op for CLI mode
}

func checkStdin() bool {
	stat, _ := os.Stdin.Stat()
	return (stat.Mode() & os.ModeCharDevice) == 0
}
