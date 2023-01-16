//go:build !(android || ios)
// +build !android,!ios

package gui

import (
	"container/ring"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/alexballas/go2tv/httphandlers"
	"github.com/alexballas/go2tv/soapcalls"
)

// NewScreen .
type NewScreen struct {
	mu                   sync.RWMutex
	muError              sync.RWMutex
	Current              fyne.Window
	VolumeDown           *widget.Button
	MediaText            *widget.Entry
	Debug                *debugWriter
	tabs                 *container.AppTabs
	CheckVersion         *widget.Button
	SubsText             *widget.Entry
	CustomSubsCheck      *widget.Check
	PlayPause            *widget.Button
	Stop                 *widget.Button
	DeviceList           *widget.List
	httpserver           *httphandlers.HTTPserver
	httpNexterver        *httphandlers.HTTPserver
	ExternalMediaURL     *widget.Check
	MuteUnmute           *widget.Button
	VolumeUp             *widget.Button
	tvdata               *soapcalls.TVPayload
	selectedDevice       devType
	currentmfolder       string
	mediafile            string
	eventlURL            string
	controlURL           string
	renderingControlURL  string
	connectionManagerURL string
	State                string
	version              string
	subsfile             string
	mediaFormats         []string
	Transcode            bool
	ErrorVisible         bool
	NextMedia            bool
	Medialoop            bool
	Hotkeys              bool
}

type debugWriter struct {
	ring *ring.Ring
}

type devType struct {
	name string
	addr string
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
func Start(ctx context.Context, s *NewScreen) {
	w := s.Current
	tabs := container.NewAppTabs(
		container.NewTabItem("Go2TV", container.NewPadded(mainWindow(s))),
		container.NewTabItem("Settings", container.NewPadded(settingsWindow(s))),
		container.NewTabItem("About", aboutWindow(s)),
	)

	s.Hotkeys = true
	tabs.OnSelected = func(t *container.TabItem) {
		theme := fyne.CurrentApp().Preferences().StringWithFallback("Theme", "Default")
		fyne.CurrentApp().Settings().SetTheme(go2tvTheme{theme})

		if t.Text == "Go2TV" {
			s.Hotkeys = true
			return
		}
		s.Hotkeys = false
	}

	s.tabs = tabs

	w.SetContent(tabs)
	w.Resize(fyne.NewSize(w.Canvas().Size().Width, w.Canvas().Size().Height*1.2))
	w.CenterOnScreen()
	w.SetMaster()

	go func() {
		<-ctx.Done()
		os.Exit(0)
	}()

	w.ShowAndRun()

}

// EmitMsg Method to implement the screen interface
func (p *NewScreen) EmitMsg(a string) {
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
func (p *NewScreen) Fini() {
	gaplessOption := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")

	if p.NextMedia && gaplessOption == "Disabled" {
		p.MediaText.Text, p.mediafile = getNextMedia(p)
		p.MediaText.Refresh()

		if !p.CustomSubsCheck.Checked {
			selectSubs(p.mediafile, p)
		}

		playAction(p)
	}
	// Main media loop logic
	if p.Medialoop {
		playAction(p)
	}
}

// InitFyneNewScreen .
func InitFyneNewScreen(v string) *NewScreen {
	go2tv := app.NewWithID("com.alexballas.go2tv")
	w := go2tv.NewWindow("Go2TV")
	currentdir, err := os.Getwd()
	if err != nil {
		currentdir = ""
	}

	theme := fyne.CurrentApp().Preferences().StringWithFallback("Theme", "Default")
	fyne.CurrentApp().Settings().SetTheme(go2tvTheme{theme})

	dw := &debugWriter{
		ring: ring.New(1000),
	}

	return &NewScreen{
		Current:        w,
		currentmfolder: currentdir,
		mediaFormats:   []string{".mp4", ".avi", ".mkv", ".mpeg", ".mov", ".webm", ".m4v", ".mpv", ".dv", ".mp3", ".flac", ".wav", ".m4a", ".jpg", ".jpeg", ".png"},
		version:        v,
		Debug:          dw,
	}
}

func check(s *NewScreen, err error) {
	s.muError.Lock()
	defer s.muError.Unlock()

	if err != nil && !s.ErrorVisible {
		s.ErrorVisible = true
		cleanErr := strings.ReplaceAll(err.Error(), ": ", "\n")
		e := dialog.NewError(errors.New(cleanErr), s.Current)
		e.Show()
		e.SetOnClosed(func() {
			s.ErrorVisible = false
		})
	}
}

func getNextMedia(screen *NewScreen) (string, string) {
	filedir := filepath.Dir(screen.mediafile)
	filelist, err := os.ReadDir(filedir)
	check(screen, err)

	var (
		breaknext                    bool
		totalMedia, counter          int
		firstMedia, resName, resPath string
	)

	for _, f := range filelist {
		isMedia := false
		for _, vext := range screen.mediaFormats {
			if filepath.Ext(filepath.Join(filedir, f.Name())) == vext {

				if firstMedia == "" {
					firstMedia = f.Name()
				}

				isMedia = true
				break
			}
		}

		if !isMedia {
			continue
		}

		totalMedia += 1
	}

	for _, f := range filelist {
		isMedia := false
		for _, vext := range screen.mediaFormats {
			if filepath.Ext(filepath.Join(filedir, f.Name())) == vext {
				isMedia = true
				break
			}
		}

		if !isMedia {
			continue
		}

		counter += 1

		if f.Name() == filepath.Base(screen.mediafile) {
			if totalMedia == counter {
				// start over
				resName = firstMedia
				resPath = filepath.Join(filedir, firstMedia)
			}

			breaknext = true
			continue
		}

		if breaknext {
			resName = f.Name()
			resPath = filepath.Join(filedir, f.Name())
			break
		}
	}

	return resName, resPath
}

func selectSubs(v string, screen *NewScreen) {
	name, path := getNextPossibleSubs(v, screen)
	screen.SubsText.Text = name
	screen.subsfile = path
	screen.SubsText.Refresh()
}

func getNextPossibleSubs(v string, screen *NewScreen) (string, string) {
	var name, path string

	possibleSub := v[0:len(v)-
		len(filepath.Ext(v))] + ".srt"

	if _, err := os.Stat(possibleSub); err == nil {
		name = filepath.Base(possibleSub)
		path = possibleSub
	}

	return name, path
}

func setPlayPauseView(s string, screen *NewScreen) {
	screen.PlayPause.Enable()
	switch s {
	case "Play":
		screen.PlayPause.Text = "Play"
		screen.PlayPause.Icon = theme.MediaPlayIcon()
		screen.PlayPause.Refresh()
	case "Pause":
		screen.PlayPause.Text = "Pause"
		screen.PlayPause.Icon = theme.MediaPauseIcon()
		screen.PlayPause.Refresh()
	}
}

func setMuteUnmuteView(s string, screen *NewScreen) {
	switch s {
	case "Mute":
		screen.MuteUnmute.Icon = theme.VolumeMuteIcon()
		screen.MuteUnmute.Refresh()
	case "Unmute":
		screen.MuteUnmute.Icon = theme.VolumeUpIcon()
		screen.MuteUnmute.Refresh()
	}
}

// updateScreenState updates the screen state based on
// the emitted messages. The State variable is used across
// the GUI interface to control certain flows.
func (p *NewScreen) updateScreenState(a string) {
	p.mu.Lock()
	p.State = a
	p.mu.Unlock()
}

// getScreenState returns the current screen state
func (p *NewScreen) getScreenState() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.State
}
