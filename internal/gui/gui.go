package gui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/alexballas/go2tv/internal/httphandlers"
	"github.com/alexballas/go2tv/internal/soapcalls"
	"github.com/alexballas/go2tv/internal/utils"
	"github.com/pkg/errors"
)

// NewScreen .
type NewScreen struct {
	App                 fyne.App
	Current             fyne.Window
	Play                *widget.Button
	Pause               *widget.Button
	Stop                *widget.Button
	Mute                *widget.Button
	Unmute              *widget.Button
	CheckVersion        *widget.Button
	CustomSubsCheck     *widget.Check
	MediaText           *widget.Entry
	SubsText            *widget.Entry
	DeviceList          *widget.List
	Medialoop           bool
	NextMedia           bool
	State               string
	mediafile           filestruct
	subsfile            filestruct
	tvdata              *soapcalls.TVPayload
	controlURL          string
	eventlURL           string
	renderingControlURL string
	currentmfolder      string
	mu                  sync.Mutex
	httpserver          *httphandlers.HTTPserver
	mediaFormats        []string
	version             string
}

type devType struct {
	name string
	addr string
}

type filestruct struct {
	abs        string
	urlEncoded string
}

type mainButtonsLayout struct{}

// Start .
func Start(s *NewScreen) {
	w := s.Current

	tabs := container.NewAppTabs(
		container.NewTabItem("Go2TV", container.NewPadded(mainWindow(s))),
		container.NewTabItem("About", aboutWindow(s)),
	)
	w.SetContent(tabs)
	w.Resize(fyne.NewSize(w.Canvas().Size().Width*1.4, w.Canvas().Size().Height*1.3))
	w.CenterOnScreen()
	w.ShowAndRun()
	os.Exit(0)
}

// EmitMsg Method to implement the screen interface
func (p *NewScreen) EmitMsg(a string) {
	switch a {
	case "Playing":
		p.Pause.Show()
		p.Play.Hide()
		p.Play.Enable()
		p.updateScreenState("Playing")
	case "Paused":
		p.Play.Show()
		p.Pause.Hide()
		p.Pause.Enable()
		p.updateScreenState("Paused")
	case "Stopped":
		p.Play.Show()
		p.Pause.Hide()
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
	if p.NextMedia {
		selectNextMedia(p)
	}
	// Main media loop logic
	if p.Medialoop {
		playAction(p)
	}
}

//InitFyneNewScreen .
func InitFyneNewScreen(v string) *NewScreen {
	go2tv := app.New()
	w := go2tv.NewWindow("Go2TV")
	currentdir, err := os.Getwd()
	if err != nil {
		currentdir = ""
	}

	return &NewScreen{
		App:            go2tv,
		Current:        w,
		currentmfolder: currentdir,
		mediaFormats:   []string{".mp4", ".avi", ".mkv", ".mpeg", ".mov", ".webm", ".m4v", ".mpv", ".mp3", ".flac"},
		version:        v,
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
func (p *NewScreen) updateScreenState(a string) {
	p.mu.Lock()
	p.State = a
	p.mu.Unlock()
}

func selectNextMedia(screen *NewScreen) {
	w := screen.Current
	filedir := filepath.Dir(screen.mediafile.abs)
	filelist, err := os.ReadDir(filedir)
	check(w, err)

	breaknext := false
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

		if f.Name() == filepath.Base(screen.mediafile.abs) {
			breaknext = true
			continue
		}

		if breaknext {
			screen.MediaText.Text = f.Name()
			screen.mediafile = filestruct{
				abs:        filepath.Join(filedir, f.Name()),
				urlEncoded: utils.ConvertFilename(f.Name()),
			}
			screen.MediaText.Refresh()

			if !screen.CustomSubsCheck.Checked {
				selectSubs(screen.mediafile.abs, screen)
			}
			break
		}
	}
}

func selectSubs(v string, screen *NewScreen) {
	possibleSub := v[0:len(v)-
		len(filepath.Ext(v))] + ".srt"

	if _, err := os.Stat(possibleSub); os.IsNotExist(err) {
		screen.SubsText.Text = ""
		screen.subsfile = filestruct{}
	} else {
		screen.SubsText.Text = filepath.Base(possibleSub)

		screen.subsfile = filestruct{
			abs:        possibleSub,
			urlEncoded: utils.ConvertFilename(possibleSub),
		}
	}
	screen.SubsText.Refresh()
}
