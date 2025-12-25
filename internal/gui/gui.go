//go:build !(android || ios)

package gui

import (
	"container/ring"
	"context"
	"embed"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/alexballas/go2tv/devices"
	"github.com/alexballas/go2tv/httphandlers"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/alexballas/go2tv/soapcalls/utils"
)

// FyneScreen .
type FyneScreen struct {
	tempFiles             []string
	SelectInternalSubs    *widget.Select
	CurrentPos            binding.String
	EndPos                binding.String
	serverStopCTX         context.Context
	Current               fyne.Window
	cancelEnablePlay      context.CancelFunc
	PlayPause             *widget.Button
	Debug                 *debugWriter
	VolumeUp              *widget.Button
	SkipNextButton        *widget.Button
	tvdata                *soapcalls.TVPayload
	tabs                  *container.AppTabs
	CheckVersion          *widget.Button
	SubsText              *widget.Entry
	CustomSubsCheck       *widget.Check
	NextMediaCheck        *widget.Check
	TranscodeCheckBox     *widget.Check
	Stop                  *widget.Button
	DeviceList            *deviceList
	httpserver            *httphandlers.HTTPserver
	MediaText             *widget.Entry
	ExternalMediaURL      *widget.Check
	SkinNextOnlySameTypes bool
	GaplessMediaWatcher   func(context.Context, *FyneScreen, *soapcalls.TVPayload)
	SlideBar              *tappedSlider
	MuteUnmute            *widget.Button
	VolumeDown            *widget.Button
	selectedDevice        devType
	State                 string
	mediafile             string
	version               string
	eventlURL             string
	subsfile              string
	controlURL            string
	renderingControlURL   string
	connectionManagerURL  string
	currentmfolder        string
	ffmpegPath            string
	ffmpegSeek            int
	systemTheme           fyne.ThemeVariant
	mediaFormats          []string
	audioFormats          []string
	videoFormats          []string
	imageFormats          []string
	muError               sync.RWMutex
	mu                    sync.RWMutex
	ffmpegPathChanged     bool
	Medialoop             bool
	sliderActive          bool
	Transcode             bool
	ErrorVisible          bool
	Hotkeys               bool
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

//go:embed translations
var translations embed.FS

// Start .
func Start(ctx context.Context, s *FyneScreen) {
	if s == nil {
		return
	}

	if s.tempFiles == nil {
		s.tempFiles = make([]string, 0)
	}

	defer func() {
		for _, file := range s.tempFiles {
			os.Remove(file)
		}
	}()

	w := s.Current
	w.SetOnDropped(onDropFiles(s))

	tabs := container.NewAppTabs(
		container.NewTabItem("Go2TV", container.NewPadded(mainWindow(s))),
		container.NewTabItem(lang.L("Settings"), container.NewPadded(settingsWindow(s))),
		container.NewTabItem(lang.L("About"), aboutWindow(s)),
	)

	s.Hotkeys = true
	tabs.OnSelected = func(t *container.TabItem) {
		if t.Text == "Go2TV" {
			s.Hotkeys = true
			s.TranscodeCheckBox.Enable()

			if err := utils.CheckFFmpeg(s.ffmpegPath); err != nil {
				s.TranscodeCheckBox.SetChecked(false)
				s.TranscodeCheckBox.Disable()
				s.SelectInternalSubs.Options = []string{}
				s.SelectInternalSubs.PlaceHolder = lang.L("No Embedded Subs")
				s.SelectInternalSubs.ClearSelected()
				s.SelectInternalSubs.Disable()
			}

			if s.ffmpegPathChanged {
				furi, err := storage.ParseURI("file://" + s.mediafile)
				if err == nil {
					go fyne.Do(func() {
						selectMediaFile(s, furi)
					})
				}
				s.ffmpegPathChanged = false
			}

			return
		}
		s.Hotkeys = false
	}

	s.ffmpegPathChanged = false
	if err := utils.CheckFFmpeg(s.ffmpegPath); err != nil {
		s.TranscodeCheckBox.Disable()
	}

	s.tabs = tabs

	w.SetContent(tabs)
	w.Resize(fyne.NewSize(w.Canvas().Size().Width, w.Canvas().Size().Height*1.2))
	w.CenterOnScreen()
	w.SetMaster()

	// Start Chromecast discovery in background
	go devices.StartChromecastDiscoveryLoop(ctx)

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
	gaplessOption := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")

	if p.NextMediaCheck.Checked && gaplessOption == "Disabled" {
		p.MediaText.Text, p.mediafile = getNextMedia(p)
		fyne.Do(func() {
			p.MediaText.Refresh()
		})

		if !p.CustomSubsCheck.Checked {
			autoSelectNextSubs(p.mediafile, p)
		}

		playAction(p)
	}
	// Main media loop logic
	if p.Medialoop {
		playAction(p)
	}
}

func initFyneNewScreen(version string) *FyneScreen {
	go2tv := app.NewWithID("app.go2tv.go2tv")

	// Hack. Ongoing discussion in https://github.com/fyne-io/fyne/issues/5333
	var content []byte
	switch go2tv.Preferences().String("Language") {
	case "中文(简体)":
		content, _ = translations.ReadFile("translations/zh.json")
	case "English":
		content, _ = translations.ReadFile("translations/en.json")
	}

	if content != nil {
		name := lang.SystemLocale().LanguageString()
		lang.AddTranslations(fyne.NewStaticResource(name+".json", content))
	} else {
		lang.AddTranslationsFS(translations, "translations")
	}

	go2tv.SetIcon(fyne.NewStaticResource("icon", go2TVIcon512))

	w := go2tv.NewWindow("Go2TV")
	currentDir, err := os.Getwd()
	if err != nil {
		currentDir = ""
	}

	dw := &debugWriter{
		ring: ring.New(1000),
	}

	return &FyneScreen{
		Current:        w,
		currentmfolder: currentDir,
		mediaFormats:   []string{".mp4", ".avi", ".mkv", ".mpeg", ".mov", ".webm", ".m4v", ".mpv", ".dv", ".mp3", ".flac", ".wav", ".m4a", ".jpg", ".jpeg", ".png"},
		imageFormats:   []string{".jpg", ".jpeg", ".png"},
		videoFormats:   []string{".mp4", ".avi", ".mkv", ".mpeg", ".mov", ".webm", ".m4v", ".mpv", ".dv"},
		audioFormats:   []string{".mp3", ".flac", ".wav", ".m4a"},
		version:        version,
		Debug:          dw,
	}
}

func check(s *FyneScreen, err error) {
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

func getNextMedia(screen *FyneScreen) (string, string) {
	filedir := filepath.Dir(screen.mediafile)
	filelist, err := os.ReadDir(filedir)
	check(screen, err)

	files := make([]string, 0)
	getType := func(s string) string {
		switch {
		case slices.Contains(screen.imageFormats, filepath.Ext(s)):
			return "image"
		case slices.Contains(screen.videoFormats, filepath.Ext(s)):
			return "video"
		case slices.Contains(screen.audioFormats, filepath.Ext(s)):
			return "audio"
		}
		return ""
	}

	for _, f := range filelist {
		fullPath := filepath.Join(filedir, f.Name())

		if !slices.Contains(screen.mediaFormats, filepath.Ext(fullPath)) {
			continue
		}

		if screen.SkinNextOnlySameTypes && getType(screen.mediafile) != getType(fullPath) {
			continue
		}

		files = append(files, f.Name())
	}

	idx := slices.Index(files, filepath.Base(screen.mediafile))
	if idx+1 == len(files) {
		return files[0], filepath.Join(filedir, files[0])
	}

	return files[idx+1], filepath.Join(filedir, files[idx+1])
}

func autoSelectNextSubs(v string, screen *FyneScreen) {
	name, path := getNextPossibleSubs(v)
	screen.SubsText.Text = name
	screen.subsfile = path
	fyne.Do(func() {
		screen.SubsText.Refresh()
	})
}

func getNextPossibleSubs(v string) (string, string) {
	var name, path string

	possibleSub := v[0:len(v)-
		len(filepath.Ext(v))] + ".srt"

	if _, err := os.Stat(possibleSub); err == nil {
		name = filepath.Base(possibleSub)
		path = possibleSub
	}

	return name, path
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

// NewFyneScreen .
func NewFyneScreen(version string) *FyneScreen {
	return initFyneNewScreen(version)
}

func onDropFiles(screen *FyneScreen) func(p fyne.Position, u []fyne.URI) {
	return func(p fyne.Position, u []fyne.URI) {
		var mfiles, sfiles []fyne.URI

	out:
		for _, f := range u {
			if strings.HasSuffix(strings.ToUpper(f.Name()), ".SRT") {
				sfiles = append(sfiles, f)
				continue
			}

			for _, s := range screen.mediaFormats {
				if strings.HasSuffix(strings.ToUpper(f.Name()), strings.ToUpper(s)) {
					mfiles = append(mfiles, f)
					continue out
				}
			}
		}

		if len(sfiles) > 0 {
			screen.CustomSubsCheck.SetChecked(true)
			selectSubsFile(screen, sfiles[0])
		}

		if len(mfiles) > 0 {
			selectMediaFile(screen, mfiles[0])
		}
	}
}
