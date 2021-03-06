package gui

import (
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	"github.com/pkg/errors"
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
	NextVideo       bool
	State           string
	videofile       filestruct
	subsfile        filestruct
	tvdata          *soapcalls.TVPayload
	transportURL    string
	controlURL      string
	currentvfolder  string
	mu              sync.Mutex
	httpserver      *httphandlers.HTTPserver
	videoFormats    []string
}

type devType struct {
	name string
	addr string
}

type filestruct struct {
	abs        string
	urlEncoded string
}

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

	vfile := widget.NewButton("Select Video File", func() {
		go videoAction(s)
	})

	vfiletext.Disable()

	sfile := widget.NewButton("Select Subtitle File", func() {
		go subsAction(s)
	})

	sfile.Disable()
	sfiletext.Disable()

	play := widget.NewButtonWithIcon("Play", theme.MediaPlayIcon(), func() {
		go playAction(s)
	})
	pause := widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), func() {
		go pauseAction(s)
	})
	stop := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), func() {
		go stopAction(s)
	})

	sfilecheck := widget.NewCheck("Custom Subtitles", func(b bool) {})
	videoloop := widget.NewCheck("Loop Selected Video", func(b bool) {})
	nextvideo := widget.NewCheck("Auto-Select Next Video", func(b bool) {})

	videofilelabel := canvas.NewText("Video:", theme.ForegroundColor())
	subsfilelabel := canvas.NewText("Subtitle:", theme.ForegroundColor())
	devicelabel := canvas.NewText("Select Device:", theme.ForegroundColor())
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
	checklists := container.NewHBox(sfilecheck, videoloop, nextvideo)
	videosubsbuttons := container.New(layout.NewGridLayout(2), vfile, sfile)
	viewfilescont := container.New(layout.NewFormLayout(), videofilelabel, vfiletext, subsfilelabel, sfiletext)

	buttons := container.NewVBox(videosubsbuttons, viewfilescont, checklists, playpausestop, devicelabel)
	content := container.New(layout.NewBorderLayout(buttons, nil, nil, nil), buttons, list)

	// Widgets actions
	list.OnSelected = func(id widget.ListItemID) {
		play.Enable()
		pause.Enable()
		t, c, err := soapcalls.DMRextractor(data[id].addr)
		s.transportURL, s.controlURL = t, c
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

	nextvideo.OnChanged = func(b bool) {
		if b {
			s.NextVideo = true
		} else {
			s.NextVideo = false
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
	w.Resize(fyne.NewSize(w.Canvas().Size().Width*1.4, w.Canvas().Size().Height*1.6))
	w.CenterOnScreen()
	w.ShowAndRun()
	os.Exit(0)
}

func videoAction(screen *NewScreen) {
	w := screen.Current
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		check(w, err)

		if reader == nil {
			return
		}
		defer reader.Close()

		vfile := reader.URI().Path()
		absVideoFile, err := filepath.Abs(vfile)
		check(w, err)

		videoFileURLencoded := &url.URL{Path: filepath.Base(absVideoFile)}
		screen.VideoText.Text = filepath.Base(vfile)
		screen.videofile = filestruct{
			abs:        absVideoFile,
			urlEncoded: videoFileURLencoded.String(),
		}

		if !screen.CustomSubsCheck.Checked {
			selectSubs(absVideoFile, screen)
		}

		// Remember the last file location.
		screen.currentvfolder = filepath.Dir(absVideoFile)

		screen.VideoText.Refresh()
	}, w)

	fd.SetFilter(storage.NewExtensionFileFilter(screen.videoFormats))

	if screen.currentvfolder != "" {
		vfileURI := storage.NewFileURI(screen.currentvfolder)
		vfileLister, err := storage.ListerForURI(vfileURI)
		check(w, err)
		fd.SetLocation(vfileLister)
	}

	fd.Resize(fyne.NewSize(w.Canvas().Size().Width*1.4, w.Canvas().Size().Height*1.6))
	fd.Show()
}

func subsAction(screen *NewScreen) {
	w := screen.Current
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		check(w, err)

		if reader == nil {
			return
		}
		defer reader.Close()

		sfile := reader.URI().Path()
		absSubtitlesFile, err := filepath.Abs(sfile)
		subsFileURLencoded := &url.URL{Path: filepath.Base(absSubtitlesFile)}
		check(w, err)

		screen.SubsText.Text = filepath.Base(sfile)
		screen.subsfile = filestruct{
			abs:        absSubtitlesFile,
			urlEncoded: subsFileURLencoded.String(),
		}
		screen.SubsText.Refresh()
	}, w)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".srt"}))

	if screen.currentvfolder != "" {
		vfileURI := storage.NewFileURI(screen.currentvfolder)
		vfileLister, err := storage.ListerForURI(vfileURI)
		check(w, err)
		fd.SetLocation(vfileLister)
	}
	fd.Resize(fyne.NewSize(w.Canvas().Size().Width*1.4, w.Canvas().Size().Height*1.6))

	fd.Show()
}

func playAction(screen *NewScreen) {
	w := screen.Current
	screen.Play.Disable()

	if screen.State == "Paused" {
		err := screen.tvdata.SendtoTV("Play")
		check(w, err)
		return
	}
	if screen.videofile.urlEncoded == "" {
		check(w, errors.New("please select a video file"))
		screen.Play.Enable()
		return
	}
	if screen.transportURL == "" {
		check(w, errors.New("please select a device"))
		screen.Play.Enable()
		return
	}

	if screen.tvdata == nil {
		stopAction(screen)
	}

	whereToListen, err := iptools.URLtoListenIPandPort(screen.transportURL)
	check(w, err)

	screen.tvdata = &soapcalls.TVPayload{
		TransportURL:  screen.transportURL,
		ControlURL:    screen.controlURL,
		VideoURL:      "http://" + whereToListen + "/" + screen.videofile.urlEncoded,
		SubtitlesURL:  "http://" + whereToListen + "/" + screen.subsfile.urlEncoded,
		CallbackURL:   "http://" + whereToListen + "/callback",
		CurrentTimers: make(map[string]*time.Timer),
	}

	screen.httpserver = httphandlers.NewServer(whereToListen)
	serverStarted := make(chan struct{})

	// We pass the tvdata here as we need the callback handlers to be able to react
	// to the different media renderer states.
	go func() {
		err := screen.httpserver.ServeFiles(serverStarted, screen.videofile.abs, screen.subsfile.abs, screen.tvdata, screen)
		if err != nil {
			check(w, err)
		}
	}()
	// Wait for the HTTP server to properly initialize.
	<-serverStarted
	err = screen.tvdata.SendtoTV("Play1")
	check(w, err)
	if err != nil {
		// Something failed when sent Play1 to the TV.
		// Just force the user to re-select a device.
		lsize := screen.DeviceList.Length()
		for i := 0; i <= lsize; i++ {
			screen.DeviceList.Unselect(lsize - 1)
			screen.DeviceList.Refresh()
		}
		screen.transportURL = ""
		stopAction(screen)
	}
}
func pauseAction(screen *NewScreen) {
	w := screen.Current
	screen.Pause.Disable()

	err := screen.tvdata.SendtoTV("Pause")
	check(w, err)
}

func stopAction(screen *NewScreen) {
	w := screen.Current

	screen.Play.Enable()
	screen.Pause.Enable()

	if screen.tvdata == nil {
		return
	}
	err := screen.tvdata.SendtoTV("Stop")

	// Hack to avoid potential http errors during video loop mode.
	// Will keep the window clean during unattended usage.
	if screen.Videoloop {
		err = nil
	}
	check(w, err)

	screen.httpserver.StopServeFiles()
	screen.tvdata = nil
	// In theory we should expect an emit message
	// from the media renderer, but there seems
	// to be a race condition that prevents this.
	screen.EmitMsg("Stopped")

}

func getDevices(delay int) (dev []devType, err error) {
	if err := devices.LoadSSDPservices(delay); err != nil {
		return nil, errors.Wrap(err, "getDevices error")
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
	if p.NextVideo {
		selectNextVideo(p)
	}
	// Main video loop logic
	if p.Videoloop {
		playAction(p)
	}
}

//InitFyneNewScreen .
func InitFyneNewScreen() *NewScreen {
	go2tv := app.New()
	app := go2tv.NewWindow("Go2TV")
	return &NewScreen{
		Current:      app,
		videoFormats: []string{".mp4", ".avi", ".mkv", ".mpeg", ".mov", ".webm", ".m4v", ".mpv"},
	}
}

func check(win fyne.Window, err error) {
	if err != nil {
		cleanErr := strings.ReplaceAll(err.Error(), ": ", "\n")
		dialog.ShowError(errors.New(cleanErr), win)
	}
}

// UpdateScreenState updates the screen state based on
// the emitted messages. The State variable is used across
// the GUI interface to control certain flows.
func (p *NewScreen) UpdateScreenState(a string) {
	p.mu.Lock()
	p.State = a
	p.mu.Unlock()
}

func selectNextVideo(screen *NewScreen) {
	w := screen.Current
	filedir := filepath.Dir(screen.videofile.abs)
	filelist, err := os.ReadDir(filedir)
	check(w, err)

	breaknext := false
	for _, f := range filelist {
		isVideo := false
		for _, vext := range screen.videoFormats {
			if filepath.Ext(filepath.Join(filedir, f.Name())) == vext {
				isVideo = true
				break
			}
		}

		if !isVideo {
			continue
		}

		if f.Name() == filepath.Base(screen.videofile.abs) {
			breaknext = true
			continue
		}

		if breaknext {
			videoFileURLencoded := &url.URL{Path: f.Name()}
			screen.VideoText.Text = f.Name()
			screen.videofile = filestruct{
				abs:        filepath.Join(filedir, f.Name()),
				urlEncoded: videoFileURLencoded.String(),
			}
			screen.VideoText.Refresh()

			if !screen.CustomSubsCheck.Checked {
				selectSubs(screen.videofile.abs, screen)
			}
			break
		}
	}
}

func selectSubs(v string, screen *NewScreen) {
	possibleSub := (v)[0:len(v)-
		len(filepath.Ext(v))] + ".srt"

	if _, err := os.Stat(possibleSub); os.IsNotExist(err) {
		screen.SubsText.Text = ""
		screen.subsfile = filestruct{}
	} else {
		subsFileURLencoded := &url.URL{Path: filepath.Base(possibleSub)}
		screen.SubsText.Text = filepath.Base(possibleSub)

		screen.subsfile = filestruct{
			abs:        possibleSub,
			urlEncoded: subsFileURLencoded.String(),
		}
	}
	screen.SubsText.Refresh()
}
