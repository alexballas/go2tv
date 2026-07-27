//go:build android || ios

package gui

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/app"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	"github.com/pkg/errors"
	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/crashlog"
	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

// FyneScreen .
type FyneScreen struct {
	mu                     sync.RWMutex
	Debug                  *debugWriter
	DiscoveryDebug         *debugWriter
	Current                fyne.Window
	tvdata                 *soapcalls.TVPayload
	chromecastClient       *castprotocol.CastClient
	chromecastActionID     uint64
	Stop                   *widget.Button
	MuteUnmute             *widget.Button
	CheckVersion           *widget.Button
	CustomSubsCheck        *widget.Check
	ExternalMediaURL       *widget.Check
	cancelEnablePlay       context.CancelFunc
	serverStopCTX          context.Context
	cancelServerStop       context.CancelFunc
	MediaText              *widget.Entry
	SubsText               *widget.Entry
	DeviceList             *deviceList
	httpserver             *httphandlers.HTTPserver
	PlayPause              *widget.Button
	TranscodeCheckBox      *widget.Check
	mediafile              fyne.URI
	subsfile               fyne.URI
	selectedDevice         devType
	selectedDeviceType     string
	NextMediaCheck         *widget.Check
	State                  string
	controlURL             string
	eventlURL              string
	renderingControlURL    string
	connectionManagerURL   string
	version                string
	mediaFormats           []string
	tempMediaFile          string // Temp file path for mobile media serving (cleanup on stop)
	tempSubsFile           string // Temp subtitle path for ffmpeg burn-in (cleanup on stop)
	ffmpegPath             string
	mediaDuration          float64
	currentArtwork         *metadata.ArtworkAsset
	currentArtworkIdentity string
	artworkCache           map[string]artworkCacheEntry
	Transcode              bool
	Medialoop              bool
	castingMediaType       string // MIME type of currently casting media
	hotkeysSuspendCount    int32
	Crash                  *crashlog.Session
	PendingCrashPath       string
}

type devType struct {
	name        string
	addr        string
	deviceType  string
	isAudioOnly bool
}

// Start .
func Start(ctx context.Context, s *FyneScreen) {
	w := s.Current

	// Clean up orphaned temp files from previous crashes
	cleanupMobileCacheTempFiles()

	// Install the Android multicast hooks before the discovery goroutines run
	// their immediate first scans.
	prepareBackgroundSession(s)
	devices.StartDiscovery(ctx)

	if app := fyne.CurrentApp(); app != nil {
		app.Lifecycle().SetOnStopped(func() {
			if s.Crash != nil {
				_ = s.Crash.CloseClean()
			}
		})
	}

	tabs := container.NewAppTabs(
		container.NewTabItem("Go2TV", container.NewVScroll(container.NewPadded(mainWindow(s)))),
		container.NewTabItem("About", container.NewVScroll(aboutWindow(s))),
	)

	w.SetContent(tabs)
	w.CenterOnScreen()

	registerShareHandler(s)

	go func() {
		<-ctx.Done()
		if s.Crash != nil {
			_ = s.Crash.CloseClean()
		}
		os.Exit(0)
	}()

	go silentCheckVersion(s)
	showPendingCrashPopup(s)

	w.ShowAndRun()
}

// EmitMsg Method to implement the screen interface
func (p *FyneScreen) EmitMsg(a string) {
	switch a {
	case "Playing":
		setPlayPauseView("Pause", p)
		p.updateScreenState("Playing")
	case "Paused":
		setPlayPauseView("Play", p)
		p.updateScreenState("Paused")
	case "Stopped":
		setPlayPauseView("Play", p)
		p.updateScreenState("Stopped")
		// Clear casting media type
		p.SetMediaType("")
		stopAction(p)
	default:
		dialog.ShowInformation("?", "Unknown callback value", p.Current)
	}
}

// SetMediaType Method to implement the screen interface
func (p *FyneScreen) SetMediaType(mediaType string) {
	p.mu.Lock()
	p.castingMediaType = mediaType
	p.mu.Unlock()
}

// Fini Method to implement the screen interface.
// Will only be executed when we receive a callback message,
// not when we explicitly click the Stop button.
func (p *FyneScreen) Fini() {
	// Main media loop logic
	if p.Medialoop {
		go playAction(p)
	}
}

// updateScreenState updates the screen state based on
// the emitted messages. The State variable is used across
// the GUI interface to control certain flows.
func (p *FyneScreen) updateScreenState(a string) {
	p.mu.Lock()
	p.State = a
	p.mu.Unlock()

	// Confirmed playback backs up the eager Play-callback start, while every
	// terminal path stops the background service here. Called after the unlock:
	// it reaches into the platform and has no business doing that under a lock.
	syncBackgroundSession(p, a)
}

// getScreenState returns the current screen state
func (p *FyneScreen) getScreenState() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.State
}

func (p *FyneScreen) nextChromecastActionID() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chromecastActionID++
	return p.chromecastActionID
}

func (p *FyneScreen) isChromecastActionCurrent(actionID uint64) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.chromecastActionID == actionID
}

func setPlayPauseView(s string, screen *FyneScreen) {
	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}

	fyne.Do(func() {
		// Check if we are casting an image
		isImage := false
		screen.mu.RLock()
		if strings.HasPrefix(screen.castingMediaType, "image/") {
			isImage = true
		}
		screen.mu.RUnlock()

		if isImage {
			screen.PlayPause.Disable()
			screen.PlayPause.SetIcon(theme.FileImageIcon())
			screen.PlayPause.SetText("Image")
		} else {
			screen.PlayPause.Enable()
			switch s {
			case "Play":
				screen.PlayPause.Text = lang.L("Play")
				screen.PlayPause.Icon = theme.MediaPlayIcon()
			case "Pause":
				screen.PlayPause.Text = lang.L("Pause")
				screen.PlayPause.Icon = theme.MediaPauseIcon()
			}
		}
		screen.PlayPause.Refresh()
	})
}

func setMuteUnmuteView(s string, screen *FyneScreen) {
	fyne.Do(func() {
		switch s {
		case "Mute":
			screen.MuteUnmute.Icon = theme.VolumeUpIcon()
		case "Unmute":
			screen.MuteUnmute.Icon = theme.VolumeMuteIcon()
		}
		screen.MuteUnmute.Refresh()
	})
}

// NewFyneScreen .
func NewFyneScreen(version string, crash *crashlog.Session) *FyneScreen {
	go2tv := app.NewWithID("app.go2tv.go2tv")
	go2tv.Settings().SetTheme(go2tvTheme{"Dark"})
	go2tv.Driver().SetDisableScreenBlanking(true)

	w := go2tv.NewWindow("Go2TV")
	dw := newDebugWriter(runtimeDebugRingSize)
	discoveryDebug := newDebugWriter(discoveryDebugRingSize)
	devices.SetDiscoveryLogOutput(discoveryDebug)
	ffmpegPath, _ := utils.ResolveFFmpegPath("")

	return &FyneScreen{
		Current:          w,
		Debug:            dw,
		DiscoveryDebug:   discoveryDebug,
		ffmpegPath:       ffmpegPath,
		mediaFormats:     mediamodel.AllMediaExtensions(),
		version:          version,
		Crash:            crash,
		PendingCrashPath: crashPath(crash),
	}
}

func check(win fyne.Window, err error) {
	if err != nil {
		cleanErr := strings.ReplaceAll(err.Error(), ": ", "\n")
		fyne.Do(func() {
			dialog.ShowError(errors.New(cleanErr), win)
		})
	}
}
