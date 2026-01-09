//go:build android || ios

package gui

import (
	"container/ring"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/alexballas/go2tv/castprotocol"
	"github.com/alexballas/go2tv/devices"
	"github.com/alexballas/go2tv/httphandlers"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/pkg/errors"
)

// FyneScreen .
type FyneScreen struct {
	mu                   sync.RWMutex
	Debug                *debugWriter
	Current              fyne.Window
	tvdata               *soapcalls.TVPayload
	chromecastClient     *castprotocol.CastClient
	Stop                 *widget.Button
	MuteUnmute           *widget.Button
	CheckVersion         *widget.Button
	CustomSubsCheck      *widget.Check
	ExternalMediaURL     *widget.Check
	cancelEnablePlay     context.CancelFunc
	serverStopCTX        context.Context
	cancelServerStop     context.CancelFunc
	MediaText            *widget.Entry
	SubsText             *widget.Entry
	DeviceList           *deviceList
	httpserver           *httphandlers.HTTPserver
	PlayPause            *widget.Button
	TranscodeCheckBox    *widget.Check
	mediafile            fyne.URI
	subsfile             fyne.URI
	selectedDevice       devType
	selectedDeviceType   string
	NextMediaCheck       *widget.Check
	State                string
	controlURL           string
	eventlURL            string
	renderingControlURL  string
	connectionManagerURL string
	version              string
	mediaFormats         []string
	tempMediaFile        string // Temp file path for mobile media serving (cleanup on stop)
	Transcode            bool
	Medialoop            bool
}

type debugWriter struct {
	ring *ring.Ring
}

type devType struct {
	name       string
	addr       string
	deviceType string
}

type mainButtonsLayout struct {
	buttonHeight  float32
	buttonPadding float32
}

func (f *debugWriter) Write(b []byte) (int, error) {
	f.ring.Value = string(b)
	f.ring = f.ring.Next()
	return len(b), nil
}

// Start .
func Start(ctx context.Context, s *FyneScreen) {
	w := s.Current

	// Clean up orphaned temp files from previous crashes
	if files, err := filepath.Glob(filepath.Join(os.TempDir(), "go2tv-*")); err == nil {
		for _, f := range files {
			os.Remove(f)
		}
	}

	// Start Chromecast discovery in background
	go devices.StartChromecastDiscoveryLoop(ctx)

	tabs := container.NewAppTabs(
		container.NewTabItem("Go2TV", container.NewVScroll(container.NewPadded(mainWindow(s)))),
		container.NewTabItem("About", container.NewVScroll(aboutWindow(s))),
	)

	w.SetContent(tabs)
	w.CenterOnScreen()

	go func() {
		<-ctx.Done()
		os.Exit(0)
	}()

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
		stopAction(p)
	default:
		dialog.ShowInformation("?", "Unknown callback value", p.Current)
	}
}

// Fini Method to implement the screen interface.
// Will only be executed when we receive a callback message,
// not when we explicitly click the Stop button.
func (p *FyneScreen) Fini() {
	// Main media loop logic
	if p.Medialoop {
		playAction(p)
	}
}

func initFyneNewScreen(v string) *FyneScreen {
	go2tv := app.NewWithID("app.go2tv.go2tv")
	go2tv.Settings().SetTheme(go2tvTheme{"Dark"})

	w := go2tv.NewWindow("Go2TV")

	return &FyneScreen{
		Current:      w,
		mediaFormats: []string{".mp4", ".avi", ".mkv", ".mpeg", ".mov", ".webm", ".m4v", ".mpv", ".dv", ".mp3", ".flac", ".wav", ".m4a", ".jpg", ".jpeg", ".png"},
		version:      v,
	}
}

func check(win fyne.Window, err error) {
	if err != nil {
		cleanErr := strings.ReplaceAll(err.Error(), ": ", "\n")
		dialog.ShowError(errors.New(cleanErr), win)
	}
}

// updateScreenState updates the screen state based on
// the emitted messages. The State variable is used across
// the GUI interface to control certain flows.
func (p *FyneScreen) updateScreenState(a string) {
	p.mu.Lock()
	p.State = a
	p.mu.Unlock()
}

// getScreenState returns the current screen state
func (p *FyneScreen) getScreenState() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.State
}

func setPlayPauseView(s string, screen *FyneScreen) {
	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}

	fyne.Do(func() {
		screen.PlayPause.Enable()
	})

	switch s {
	case "Play":
		screen.PlayPause.Text = lang.L("Play")
		screen.PlayPause.Icon = theme.MediaPlayIcon()
	case "Pause":
		screen.PlayPause.Text = lang.L("Pause")
		screen.PlayPause.Icon = theme.MediaPauseIcon()
	}

	fyne.Do(func() {
		screen.PlayPause.Refresh()
	})
}

func setMuteUnmuteView(s string, screen *FyneScreen) {
	switch s {
	case "Mute":
		screen.MuteUnmute.Icon = theme.VolumeUpIcon()
	case "Unmute":
		screen.MuteUnmute.Icon = theme.VolumeMuteIcon()
	}

	fyne.Do(func() {
		screen.MuteUnmute.Refresh()
	})
}

// NewFyneScreen .
func NewFyneScreen(version string) *FyneScreen {
	return initFyneNewScreen(version)
}
