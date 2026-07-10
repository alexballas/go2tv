package main

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/cliartwork"
	"go2tv.app/go2tv/v2/internal/crashlog"
	"go2tv.app/go2tv/v2/internal/devicecolors"
	"go2tv.app/go2tv/v2/internal/gui"
	"go2tv.app/go2tv/v2/internal/interactive"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

var (
	version      = "dev"
	mediaArg     = flag.String("v", "", "Path to video/audio file (triggers CLI mode).")
	urlArg       = flag.String("u", "", "URL to media file (triggers CLI mode).")
	subsArg      = flag.String("s", "", "Path to subtitles file (.srt or .vtt).")
	artworkArg   = flag.String("artwork", "", "Path to JPEG/PNG artwork override.")
	targetPtr    = flag.String("t", "", "Device URL to cast to (from -l output).")
	transcodePtr = flag.Bool("tc", false, "Force transcoding with ffmpeg.")
	listPtr      = flag.Bool("l", false, "List available devices (Smart TVs and Chromecasts).")

	versionPtr = flag.Bool("version", false, "Print version.")

	errNoCombi = errors.New("can't combine -l with other flags")
)

type flagResults struct {
	targetURL string // Device URL (DLNA renderer or Chromecast)
	exit      bool
	gui       bool
}

func main() {
	crash, err := crashlog.Init("go2tv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: crash logging disabled: %v\n", err)
	}

	runErr := run(crash)
	if crash != nil {
		if err := crash.CloseClean(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to clean crash log state: %v\n", err)
		}
	}

	if runErr != nil {
		fmt.Fprintf(os.Stderr, "Encountered error(s): %s\n", runErr)
		os.Exit(1)
	}
}

func run(crash *crashlog.Session) error {
	var (
		absMediaFile, mediaType string
		mediaFile               any
		isSeek                  bool
		localMedia              bool
		transcode               bool
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

	if !flagRes.gui && crash != nil && crash.PreviousCrashPath() != "" {
		fmt.Fprintf(os.Stderr, "Previous crash report: %s\n", crash.PreviousCrashPath())
	}

	isChromecastTarget := devices.IsChromecastURL(flagRes.targetURL)
	transcode = *transcodePtr

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
		if isChromecastTarget {
			mediaURL, inferredMediaType, err := utils.StreamURLWithMime(context.Background(), *urlArg)
			if err != nil {
				return err
			}
			mediaURL.Close()
			mediaType = inferredMediaType
			absMediaFile = *urlArg

			if utils.IsHLSStream(*urlArg, mediaType) {
				transcode = false
			}
		} else {
			preparedMedia, inferredMediaType, err := utils.PrepareURLMedia(context.Background(), *urlArg)
			if err != nil {
				return err
			}
			mediaType = inferredMediaType

			mediaFile = preparedMedia

			if utils.IsHLSStream(*urlArg, mediaType) {
				transcode = false
			}
		}
	}

	if isChromecastTarget && *mediaArg == "" && *urlArg != "" {
		transcode = playback.ChromecastTranscodeEnabled(transcode, *urlArg, mediaType)
	}

	if transcode {
		if _, err := utils.ResolveFFmpegPath(""); err != nil {
			return fmt.Errorf("checkTCflag parse error: %w", err)
		}
	}

	if flagRes.gui {
		scr := gui.NewFyneScreen(version, crash)
		gui.Start(exitCTX, scr)
		return nil
	}

	switch t := mediaFile.(type) {
	case string:
		localMedia = true
		absMediaFile, err = filepath.Abs(t)
		if err != nil {
			return err
		}

		mediaFile = absMediaFile
		mediaType, err = utils.GetMimeDetailsFromPath(absMediaFile)
		if err != nil {
			return err
		}

		if !transcode {
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
	ffmpegPath, _ := utils.ResolveFFmpegPath("")

	// Branch based on device type
	if isChromecastTarget {
		return runChromecastCLI(exitCTX, cancel, flagRes.targetURL, absMediaFile, mediaFile, mediaType, absSubtitlesFile, *artworkArg, ffmpegPath, transcode, *mediaArg == "" && *urlArg != "")
	}

	scr, err := interactive.InitTcellNewScreen(cancel)
	if err != nil {
		return err
	}

	tvdata, err := soapcalls.NewTVPayload(&soapcalls.Options{
		Ctx:            exitCTX,
		DMR:            flagRes.targetURL,
		Media:          absMediaFile,
		Subs:           absSubtitlesFile,
		Mtype:          mediaType,
		Transcode:      transcode,
		Seek:           isSeek,
		FFmpegPath:     ffmpegPath,
		FFmpegSubsPath: absSubtitlesFile,
		FFmpegSeek:     0,
	})
	if err != nil {
		return err
	}

	s := httphandlers.NewServer(tvdata.ListenAddress())
	tvdata.Metadata.Artwork = cliartwork.PrepareWithOverride(s, absMediaFile, *artworkArg, tvdata.ListenAddress(), localMedia)
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

	interExit := make(chan error)
	go scr.InterInit(tvdata, interExit)

	select {
	case e := <-interExit:
		return e
	case <-exitCTX.Done():
	}

	return nil
}

func runChromecastCLI(ctx context.Context, cancel context.CancelFunc, deviceURL, mediaPath string, mediaFile any, mediaType, subsPath, artworkPath, ffmpegPath string, transcode, externalURL bool) error {
	if externalURL {
		transcode = playback.ChromecastExternalURLPolicy(mediaPath, mediaType, transcode, subsPath).Transcode
	} else {
		transcode = playback.ChromecastTranscodeEnabled(transcode, mediaPath, mediaType)
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

	// Create TranscodeOptions if transcoding enabled
	var tcOpts *utils.TranscodeOptions
	var mediaDuration float64
	if transcode {
		// Get actual media duration from ffprobe (Chromecast can't detect it for transcoded streams)
		// Only works for file paths, not streams
		if _, isStream := mediaFile.(io.ReadCloser); !isStream && !externalURL {
			if duration, err := utils.DurationForMediaSeconds(ffmpegPath, mediaPath); err == nil {
				mediaDuration = duration
			}
		}

		tcSubsPath := ""
		if subtitlesPath, ok := playback.ChromecastSubtitlePath(subsPath); ok {
			tcSubsPath = subtitlesPath
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

	mediaURL := mediaPath
	subtitleURL := ""
	subtitlesPath, hasSubtitles := playback.ChromecastSubtitlePath(subsPath)
	needsMediaServer := !externalURL || transcode
	_, localMedia := mediaFile.(string)
	artworkAsset := cliartwork.Resolve(mediaPath, artworkPath, localMedia)
	needsLocalServer := needsMediaServer || (hasSubtitles && !transcode) || artworkAsset != nil
	var httpServer *httphandlers.HTTPserver
	mediaMetadata := metadata.Media{Title: mediaPath}

	if needsLocalServer {
		whereToListen, err := utils.URLtoListenIPandPort(deviceURL)
		if err != nil {
			return fmt.Errorf("chromecast listen addr: %w", err)
		}

		httpServer = httphandlers.NewServer(whereToListen)
		defer httpServer.StopServer()
		mediaMetadata.Artwork = cliartwork.Register(httpServer, artworkAsset, whereToListen)

		if hasSubtitles && !transcode {
			switch strings.ToLower(filepath.Ext(subtitlesPath)) {
			case ".srt":
				webvttData, err := utils.ConvertSRTtoWebVTT(subtitlesPath)
				if err != nil {
					return fmt.Errorf("subtitle conversion: %w", err)
				}
				httpServer.AddHandler("/subtitles.vtt", nil, nil, webvttData)
			case ".vtt":
				httpServer.AddHandler("/subtitles.vtt", nil, nil, subtitlesPath)
			}
			subtitleURL = "http://" + whereToListen + "/subtitles.vtt"
		}

		serverStarted := make(chan error)
		if needsMediaServer {
			mediaFilename := "/" + utils.ConvertFilename(mediaPath)
			mediaURL = "http://" + whereToListen + mediaFilename

			if stream, isStream := mediaFile.(io.ReadCloser); isStream {
				httpServer.AddHandler(mediaFilename, nil, tcOpts, stream)

				go func() {
					httpServer.StartServing(serverStarted)
				}()
			} else if externalURL {
				stream, err := utils.StreamURL(context.Background(), mediaPath)
				if err != nil {
					return fmt.Errorf("chromecast stream url: %w", err)
				}
				httpServer.AddHandler(mediaFilename, nil, tcOpts, stream)

				go func() {
					httpServer.StartServing(serverStarted)
				}()
			} else {
				go func() {
					httpServer.StartSimpleServerWithTranscode(serverStarted, mediaPath, tcOpts)
				}()
			}
		} else {
			go func() {
				httpServer.StartServing(serverStarted)
			}()
		}

		if err := <-serverStarted; err != nil {
			return fmt.Errorf("chromecast server: %w", err)
		}
	}

	// Init interactive screen
	scr, err := interactive.InitChromecastScreen(cancel)
	if err != nil {
		return err
	}
	scr.Client = client

	// Load media (async)
	go func() {
		// Use LIVE stream type for URL/stdin streams to avoid ~30s buffering delay
		_, isStream := mediaFile.(io.ReadCloser)
		if err := client.LoadMedia(castprotocol.LoadRequest{
			MediaURL:    mediaURL,
			ContentType: mediaType,
			Metadata:    mediaMetadata,
			StartTime:   0,
			Duration:    mediaDuration,
			SubtitleURL: subtitleURL,
			Live:        externalURL || isStream,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "chromecast load: %v\n", err)
		}
	}()

	interExit := make(chan error)
	go scr.InterInit(mediaPath, interExit)

	select {
	case e := <-interExit:
		return e
	case <-ctx.Done():
	}

	return nil
}

type Device struct {
	Model string
	URL   string
	Type  string
}

type refreshMsg []Device

type listDevicesModel struct {
	devices []Device
}

var previousList []Device

func checkDevices() tea.Cmd {
	return func() tea.Msg {
		deviceList, _ := devices.LoadAllDevices()

		var rMsg refreshMsg

	outer:
		for _, old := range previousList {
			oldAddress, _ := url.Parse(old.URL)
			for _, dev := range deviceList {
				if dev.Addr == old.URL {
					continue outer
				}
			}

			if utils.HostPortIsAlive(oldAddress.Host) {
				rMsg = append(rMsg, old)
			}

		}

		for _, dev := range deviceList {
			rMsg = append(rMsg, Device{
				Model: dev.Name,
				URL:   dev.Addr,
				Type:  dev.Type,
			})
		}

		slices.SortFunc(rMsg, func(a, b Device) int {
			if a.Type != b.Type {
				return cmp.Compare(a.Type, b.Type)
			}
			return strings.Compare(a.Model, b.Model)
		})

		previousList = rMsg
		return rMsg
	}
}

func (m listDevicesModel) Init() tea.Cmd {
	devices.StartDiscovery(context.Background())
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

var (
	chromecastTag = deviceTagStyle(devices.DeviceTypeChromecast)
	dlnaTag       = deviceTagStyle(devices.DeviceTypeDLNA)
)

func deviceTagStyle(deviceType string) lipgloss.Style {
	palette := devicecolors.DarkPalette(deviceType)
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(devicecolors.Hex(palette.Text))).
		Background(lipgloss.Color(devicecolors.Hex(palette.Fill))).
		Bold(true)
}

func typeTag(t string) string {
	switch strings.ToLower(t) {
	case "chromecast":
		return chromecastTag.Render("Chromecast")
	case "dlna":
		return dlnaTag.Render("DLNA")
	default:
		return t
	}
}

func (m listDevicesModel) View() string {
	var s strings.Builder
	s.WriteString("Scanning devices... (q to quit)\n\n")
	for _, dev := range m.devices {
		s.WriteString("• " + dev.Model + " " + typeTag(dev.Type) + " [" + dev.URL + "]\n")
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

	if checkGUI() {
		res.gui = true
		return res, nil
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
		if *urlArg != "" {
			return nil
		}

		_, err := utils.ResolveFFmpegPath("")
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

func checkGUI() bool {
	return *mediaArg == "" && !*listPtr && *urlArg == "" && !checkStdin()
}

func checkStdin() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	stat, _ := os.Stdin.Stat()
	return (stat.Mode() & os.ModeCharDevice) == 0
}
