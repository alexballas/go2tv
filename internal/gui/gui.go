package gui

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/alexballas/go2tv/internal/devices"
	"github.com/alexballas/go2tv/internal/httphandlers"
	"github.com/alexballas/go2tv/internal/iptools"
	"github.com/alexballas/go2tv/internal/soapcalls"
	"github.com/gen2brain/dlgs"
)

type NewScreen struct {
	Current fyne.Window
	Play    *widget.Button
	Pause   *widget.Button
	Stop    *widget.Button
	State   string
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
)

func Start(s *NewScreen) {
	w := s.Current
	refreshDevices := time.NewTicker(20 * time.Second)

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

	vfile := widget.NewButton("Select Video File", videoAction(w, vfiletext, sfiletext))
	vfiletext.Disable()

	sfile := widget.NewButton("Select Subtitle File", subsAction(w, sfiletext))
	sfile.Disable()
	sfiletext.Disable()

	play := widget.NewButtonWithIcon("Play", theme.MediaPlayIcon(), playAction(s))
	pause := widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), pauseAction(s))
	stop := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), stopAction(s))

	s.Play = play
	s.Pause = pause
	s.Stop = stop

	pause.Hide()
	sfilecheck := widget.NewCheck("Custom Subtitles", func(b bool) {})

	devicelabel := widget.NewLabel("Select Device:")
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

	playpause := container.New(layout.NewMaxLayout(), play, pause)
	playpausestop := container.New(layout.NewGridLayout(2), playpause, stop)
	buttons := container.NewVBox(vfile, vfiletext, sfile, sfiletext, sfilecheck, playpausestop, devicelabel)

	content := container.New(layout.NewBorderLayout(buttons, nil, nil, nil), buttons, list)

	list.OnSelected = func(id widget.ListItemID) {
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

	go func() {
		for range refreshDevices.C {
			data2, err := getDevices(1)
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

func videoAction(w fyne.Window, v, s *widget.Entry) func() {
	return func() {
		vfile, _, _ := dlgs.File("Select file", "", false)
		absVideoFile, err := filepath.Abs(vfile)
		videoFileURLencoded := &url.URL{Path: filepath.Base(absVideoFile)}
		check(w, err)
		v.Text = videoFileURLencoded.String()
		videofile = filestruct{
			abs:        absVideoFile,
			urlEncoded: videoFileURLencoded.String(),
		}

		possibleSub := (absVideoFile)[0:len(absVideoFile)-
			len(filepath.Ext(absVideoFile))] + ".srt"

		if _, err = os.Stat(possibleSub); os.IsNotExist(err) {
			s.Text = ""
			subsfile = filestruct{}
		} else {
			subsFileURLencoded := &url.URL{Path: filepath.Base(possibleSub)}
			s.Text = subsFileURLencoded.String()

			subsfile = filestruct{
				abs:        possibleSub,
				urlEncoded: subsFileURLencoded.String(),
			}
		}
		v.Refresh()
		s.Refresh()
	}
}

func subsAction(w fyne.Window, s *widget.Entry) func() {
	return func() {
		sfile, _, _ := dlgs.File("Select file", "", false)
		absSubtitlesFile, err := filepath.Abs(sfile)
		subsFileURLencoded := &url.URL{Path: filepath.Base(absSubtitlesFile)}
		check(w, err)

		s.Text = subsFileURLencoded.String()
		subsfile = filestruct{
			abs:        absSubtitlesFile,
			urlEncoded: subsFileURLencoded.String(),
		}
		s.Refresh()
	}
}

func playAction(screen *NewScreen) func() {
	w := screen.Current
	return func() {
		if screen.State == "Paused" {
			err := tvdata.SendtoTV("Play")
			check(w, err)
			return
		}
		if videofile.urlEncoded == "" {
			check(w, errors.New("no video file defined"))
			return
		}
		if transportURL == "" {
			check(w, errors.New("please select a device"))
			return
		}
		if screen.State == "Transitioning" {
			return
		}
		screen.State = "Transitioning"

		if tvdata.CallbackURL != "" {
			stopAction(screen)()
		}
		whereToListen, err := iptools.URLtoListenIPandPort(transportURL)
		check(w, err)
		tvdata = &soapcalls.TVPayload{
			TransportURL: transportURL,
			ControlURL:   controlURL,
			CallbackURL:  "http://" + whereToListen + "/callback",
			VideoURL:     "http://" + whereToListen + "/" + videofile.urlEncoded,
			SubtitlesURL: "http://" + whereToListen + "/" + subsfile.urlEncoded,
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

	}
}
func pauseAction(screen *NewScreen) func() {
	w := screen.Current
	return func() {
		err := tvdata.SendtoTV("Pause")
		check(w, err)
	}
}

func stopAction(screen *NewScreen) func() {
	w := screen.Current
	return func() {
		if tvdata.CallbackURL == "" {
			return
		}
		err := tvdata.SendtoTV("Stop")
		check(w, err)
		httpserver.StopServeFiles()
		tvdata = &soapcalls.TVPayload{}
		// In theory we should expect an emit message
		// from the media renderer, but there seems
		// to be a race condition that prevents this
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

func (p *NewScreen) EmitMsg(a string) {
	switch a {
	case "Playing":
		p.Pause.Show()
		p.Play.Hide()
		p.State = "Playing"
	case "Paused":
		p.Play.Show()
		p.Pause.Hide()
		p.State = "Paused"
	case "Stopped":
		p.Play.Show()
		p.Pause.Hide()
		p.State = "Stopped"
	default:
		dialog.ShowInformation("?", "Unknown callback value", p.Current)
	}
}

func (p *NewScreen) Fini() {
}

func InitFyneNewScreen() *NewScreen {
	myApp := app.New()
	app := myApp.NewWindow("Go2TV")
	return &NewScreen{
		Current: app,
	}
}

func check(win fyne.Window, err error) {
	if err != nil {
		dialog.ShowError(err, win)
	}
}
