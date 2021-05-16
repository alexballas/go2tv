package gui

import (
	"errors"
	"image/color"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/alexballas/go2tv/internal/devices"
	"github.com/alexballas/go2tv/internal/httphandlers"
	"github.com/alexballas/go2tv/internal/iptools"
	"github.com/alexballas/go2tv/internal/soapcalls"
)

// NewScreen .
type NewScreen struct {
	Current         fyne.Window
	Play            *widget.Button
	Pause           *widget.Button
	Stop            *widget.Button
	CustomSubsCheck *widget.Check
	VideoText       *widget.Entry
	SubsText        *widget.Entry
	DeviceList      *widget.List
	Videoloop       bool
	State           string
}

type devType struct {
	name string
	addr string
}

type filestruct struct {
	abs        string
	urlEncoded string
}

var (
	videofile     = filestruct{}
	subsfile      = filestruct{}
	tvdata        = &soapcalls.TVPayload{}
	httpserver    = httphandlers.HTTPserver{}
	transportURL  = ""
	controlURL    = ""
	serverStarted = make(chan struct{})
	mu            = sync.Mutex{}
)

// Start .
func Start(s *NewScreen) {
	w := s.Current
	refreshDevices := time.NewTicker(10 * time.Second)

	list := new(widget.List)

	data := make([]devType, 0)

	go func() {
		data2, err := getDevices(1)
		data = data2
		if err != nil {
			data = nil
		}
		list.Refresh()
	}()

	vfiletext := widget.NewEntry()
	sfiletext := widget.NewEntry()

	vfile := widget.NewButton("Select Video File", videoAction(s))
	vfiletext.Disable()

	sfile := widget.NewButton("Select Subtitle File", subsAction(s))
	sfile.Disable()
	sfiletext.Disable()

	play := widget.NewButtonWithIcon("Play", theme.MediaPlayIcon(), playAction(s))
	pause := widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), pauseAction(s))
	stop := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), stopAction(s))

	sfilecheck := widget.NewCheck("Custom Subtitles", func(b bool) {})
	videoloop := widget.NewCheck("Loop Video", func(b bool) {})

	videofilelabel := canvas.NewText("Video:", color.Black)
	subsfilelabel := canvas.NewText("Subtitle:", color.Black)
	devicelabel := canvas.NewText("Select Device:", color.Black)
	pause.Hide()

	list = widget.NewList(
		func() int {
			return len(data)
		},
		func() fyne.CanvasObject {
			return container.NewHBox(widget.NewIcon(theme.NavigateNextIcon()), widget.NewLabel("Template Object"))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*fyne.Container).Objects[1].(*widget.Label).SetText(data[i].name)
		})

	s.Play = play
	s.Pause = pause
	s.Stop = stop
	s.CustomSubsCheck = sfilecheck
	s.VideoText = vfiletext
	s.SubsText = sfiletext
	s.DeviceList = list

	// Organising widgets in the window
	playpause := container.New(layout.NewMaxLayout(), play, pause)
	playpausestop := container.New(layout.NewGridLayout(2), playpause, stop)
	checklists := container.NewHBox(sfilecheck, videoloop)
	videosubsbuttons := container.New(layout.NewGridLayout(2), vfile, sfile)
	viewfilescont := container.New(layout.NewFormLayout(), videofilelabel, vfiletext, subsfilelabel, sfiletext)

	buttons := container.NewVBox(videosubsbuttons, viewfilescont, checklists, playpausestop, devicelabel)
	content := container.New(layout.NewBorderLayout(buttons, nil, nil, nil), buttons, list)

	// Widgets actions
	list.OnSelected = func(id widget.ListItemID) {
		play.Enable()
		pause.Enable()
		t, c, err := soapcalls.DMRextractor(data[id].addr)
		transportURL, controlURL = t, c
		check(w, err)
	}

	sfilecheck.OnChanged = func(b bool) {
		if b {
			sfile.Enable()
		} else {
			sfile.Disable()
		}
	}

	videoloop.OnChanged = func(b bool) {
		if b {
			s.Videoloop = true
		} else {
			s.Videoloop = false
		}
	}

	// Device list auto-refresh
	go func() {
		for range refreshDevices.C {
			data2, err := getDevices(2)
			data = data2
			if err != nil {
				data = nil
			}
			list.Refresh()
		}
	}()
	w.SetContent(content)
	w.Resize(fyne.NewSize(800, 500))
	w.ShowAndRun()
	os.Exit(0)
}

func videoAction(screen *NewScreen) func() {
	w := screen.Current
	return func() {
		fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			check(w, err)

			if reader == nil {
				return
			}

			vfile := reader.URI().Path()
			absVideoFile, err := filepath.Abs(vfile)
			check(w, err)

			videoFileURLencoded := &url.URL{Path: filepath.Base(absVideoFile)}
			screen.VideoText.Text = videoFileURLencoded.String()
			videofile = filestruct{
				abs:        absVideoFile,
				urlEncoded: videoFileURLencoded.String(),
			}
			if !screen.CustomSubsCheck.Checked {
				possibleSub := (absVideoFile)[0:len(absVideoFile)-
					len(filepath.Ext(absVideoFile))] + ".srt"

				if _, err = os.Stat(possibleSub); os.IsNotExist(err) {
					screen.SubsText.Text = ""
					subsfile = filestruct{}
				} else {
					subsFileURLencoded := &url.URL{Path: filepath.Base(possibleSub)}
					screen.SubsText.Text = subsFileURLencoded.String()

					subsfile = filestruct{
						abs:        possibleSub,
						urlEncoded: subsFileURLencoded.String(),
					}
				}
			}
			screen.VideoText.Refresh()
			screen.SubsText.Refresh()
		}, w)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".mp4", ".avi", ".mkv", ".mpeg", ".mov", ".webm", ".m4v", ".mpv"}))
		fd.Resize(fyne.NewSize(800, 500))
		fd.Show()
	}
}

func subsAction(screen *NewScreen) func() {
	w := screen.Current
	return func() {
		fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			check(w, err)

			if reader == nil {
				return
			}

			sfile := reader.URI().Path()
			absSubtitlesFile, err := filepath.Abs(sfile)
			subsFileURLencoded := &url.URL{Path: filepath.Base(absSubtitlesFile)}
			check(w, err)

			screen.SubsText.Text = subsFileURLencoded.String()
			subsfile = filestruct{
				abs:        absSubtitlesFile,
				urlEncoded: subsFileURLencoded.String(),
			}
			screen.SubsText.Refresh()
		}, w)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".srt"}))
		fd.Resize(fyne.NewSize(800, 500))
		fd.Show()
	}
}

func playAction(screen *NewScreen) func() {
	w := screen.Current
	return func() {
		screen.Play.Disable()
		if screen.State == "Paused" {
			err := tvdata.SendtoTV("Play")
			check(w, err)
			return
		}
		if videofile.urlEncoded == "" {
			check(w, errors.New("please select a video file"))
			screen.Play.Enable()
			return
		}
		if transportURL == "" {
			check(w, errors.New("please select a device"))
			screen.Play.Enable()
			return
		}

		if tvdata.CallbackURL != "" {
			stopAction(screen)()
		}

		whereToListen, err := iptools.URLtoListenIPandPort(transportURL)
		check(w, err)

		tvdata = &soapcalls.TVPayload{
			TransportURL:  transportURL,
			ControlURL:    controlURL,
			VideoURL:      "http://" + whereToListen + "/" + videofile.urlEncoded,
			SubtitlesURL:  "http://" + whereToListen + "/" + subsfile.urlEncoded,
			CallbackURL:   "http://" + whereToListen + "/callback",
			CurrentTimers: make(map[string]*time.Timer),
		}

		httpserver = httphandlers.NewServer(whereToListen)

		// We pass the tvdata here as we need the callback handlers to be able to react
		// to the different media renderer states.
		go func() {
			httpserver.ServeFiles(serverStarted, videofile.abs, subsfile.abs, &httphandlers.HTTPPayload{Soapcalls: tvdata, Screen: screen})
		}()
		// Wait for HTTP server to properly initialize
		<-serverStarted
		err = tvdata.SendtoTV("Play1")
		check(w, err)
		if err != nil {
			// Something failed when sent Play1 to the TV.
			// Just force the user to re-select a device.
			lsize := screen.DeviceList.Length()
			for i := 0; i <= lsize; i++ {
				screen.DeviceList.Unselect(lsize - 1)
				screen.DeviceList.Refresh()
			}
			transportURL = ""
			stopAction(screen)()
		}
	}
}
func pauseAction(screen *NewScreen) func() {
	w := screen.Current
	return func() {
		screen.Pause.Disable()
		err := tvdata.SendtoTV("Pause")
		check(w, err)
	}
}

func stopAction(screen *NewScreen) func() {
	w := screen.Current
	return func() {
		screen.Play.Enable()
		screen.Pause.Enable()

		if tvdata.CallbackURL == "" {
			return
		}
		err := tvdata.SendtoTV("Stop")

		// Hack to avoid potential http errors during video loop mode.
		// Will keep the window clean during unattended usage.
		if screen.Videoloop {
			err = nil
		}
		check(w, err)

		httpserver.StopServeFiles()
		tvdata = &soapcalls.TVPayload{}
		// In theory we should expect an emit message
		// from the media renderer, but there seems
		// to be a race condition that prevents this.
		screen.EmitMsg("Stopped")
	}
}

func getDevices(delay int) (dev []devType, err error) {
	if err := devices.LoadSSDPservices(delay); err != nil {
		return nil, err
	}
	// We loop through this map twice as we need to maintain
	// the correct order.
	keys := make([]int, 0)
	for k := range devices.Devices {
		keys = append(keys, k)
	}

	sort.Ints(keys)

	guiDeviceList := make([]devType, 0)
	for _, k := range keys {
		guiDeviceList = append(guiDeviceList, devType{devices.Devices[k][0], devices.Devices[k][1]})
	}
	return guiDeviceList, nil
}

// EmitMsg Method to implement the screen interface
func (p *NewScreen) EmitMsg(a string) {
	switch a {
	case "Playing":
		p.Pause.Show()
		p.Play.Hide()
		p.Play.Enable()
		p.UpdateScreenState("Playing")
	case "Paused":
		p.Play.Show()
		p.Pause.Hide()
		p.Pause.Enable()
		p.UpdateScreenState("Paused")
	case "Stopped":
		p.Play.Show()
		p.Pause.Hide()
		p.UpdateScreenState("Stopped")
	default:
		dialog.ShowInformation("?", "Unknown callback value", p.Current)
	}
}

// Fini Method to implement the screen interface.
// Will only be executed when we receive a callback message,
// not when we explicitly click the Stop button.
func (p *NewScreen) Fini() {
	// Main video loop logic
	if p.Videoloop {
		playAction(p)()
	}
}

//InitFyneNewScreen .
func InitFyneNewScreen() *NewScreen {
	go2tv := app.New()
	go2tv.Settings().SetTheme(theme.LightTheme())
	app := go2tv.NewWindow("Go2TV")
	return &NewScreen{
		Current: app,
	}
}

func check(win fyne.Window, err error) {
	if err != nil {
		dialog.ShowError(err, win)
	}
}

// UpdateScreenState updates the screen state based on
// the emitted messages. The State variable is used across
// the GUI interface to control certain flows.
func (p *NewScreen) UpdateScreenState(a string) {
	mu.Lock()
	p.State = a
	mu.Unlock()
}
