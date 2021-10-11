package gui

import (
	"fmt"
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
	"github.com/alexballas/go2tv/internal/utils"
	"github.com/pkg/errors"
)

// NewScreen .
type NewScreen struct {
	Current             fyne.Window
	Play                *widget.Button
	Pause               *widget.Button
	Stop                *widget.Button
	Mute                *widget.Button
	Unmute              *widget.Button
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

func (d *mainButtonsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w, h := float32(0), float32(0)
	for _, o := range objects {
		childSize := o.MinSize()
		w += childSize.Width
		h = childSize.Height
	}
	return fyne.NewSize(w, h)
}

func (d *mainButtonsLayout) Layout(objects []fyne.CanvasObject, containerSize fyne.Size) {
	pos := fyne.NewPos(0, 0)

	bigButtonSize := containerSize.Width
	for q, o := range objects {
		z := q + 1
		if z%2 == 0 {
			bigButtonSize = bigButtonSize - o.MinSize().Width
		}
	}
	bigButtonSize = bigButtonSize / 2

	for q, o := range objects {
		var size fyne.Size
		switch q % 2 {
		case 0:
			size = fyne.NewSize(bigButtonSize, o.MinSize().Height)
		default:
			size = o.MinSize()
		}
		o.Resize(size)
		o.Move(pos)

		pos = pos.Add(fyne.NewPos(size.Width, 0))
	}
}

// Start .
func Start(s *NewScreen) {
	w := s.Current
	refreshDevices := time.NewTicker(10 * time.Second)
	checkMute := time.NewTicker(1 * time.Second)

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

	mfiletext := widget.NewEntry()
	sfiletext := widget.NewEntry()

	mfile := widget.NewButton("Select Media File", func() {
		go mediaAction(s)
	})

	mfiletext.Disable()

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
	mute := widget.NewButtonWithIcon("", theme.VolumeMuteIcon(), func() {
		go muteAction(s)
	})
	unmute := widget.NewButtonWithIcon("", theme.VolumeUpIcon(), func() {
		go unmuteAction(s)
	})
	clearmedia := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		go clearmediaAction(s)
	})
	clearsubs := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		go clearsubsAction(s)
	})

	sfilecheck := widget.NewCheck("Custom Subtitles", func(b bool) {})
	medialoop := widget.NewCheck("Loop Selected Media File", func(b bool) {})
	nextmedia := widget.NewCheck("Auto-Select Next Media File", func(b bool) {})

	mediafilelabel := canvas.NewText("File:", theme.ForegroundColor())
	subsfilelabel := canvas.NewText("Subtitle:", theme.ForegroundColor())
	devicelabel := canvas.NewText("Select Device:", theme.ForegroundColor())
	pause.Hide()
	unmute.Hide()

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
	s.Mute = mute
	s.Unmute = unmute
	s.CustomSubsCheck = sfilecheck
	s.MediaText = mfiletext
	s.SubsText = sfiletext
	s.DeviceList = list

	// Organising widgets in the window
	playpause := container.New(layout.NewMaxLayout(), play, pause)
	muteunmute := container.New(layout.NewMaxLayout(), mute, unmute)
	playpausemutestop := container.New(&mainButtonsLayout{}, playpause, muteunmute, stop)

	checklists := container.NewHBox(sfilecheck, medialoop, nextmedia)
	mediasubsbuttons := container.New(layout.NewGridLayout(2), mfile, sfile)
	sfiletextArea := container.New(layout.NewBorderLayout(nil, nil, nil, clearsubs), clearsubs, sfiletext)
	mfiletextArea := container.New(layout.NewBorderLayout(nil, nil, nil, clearmedia), clearmedia, mfiletext)

	viewfilescont := container.New(layout.NewFormLayout(), mediafilelabel, mfiletextArea, subsfilelabel, sfiletextArea)
	buttons := container.NewVBox(mediasubsbuttons, viewfilescont, checklists, playpausemutestop, devicelabel)
	content := container.New(layout.NewBorderLayout(buttons, nil, nil, nil), buttons, list)

	// Widgets actions
	list.OnSelected = func(id widget.ListItemID) {
		play.Enable()
		pause.Enable()
		t, err := soapcalls.DMRextractor(data[id].addr)
		check(w, err)

		if err == nil {
			s.controlURL, s.eventlURL, s.renderingControlURL = t.AvtransportControlURL, t.AvtransportEventSubURL, t.RenderingControlURL
		}
	}

	sfilecheck.OnChanged = func(b bool) {
		if b {
			sfile.Enable()
		} else {
			sfile.Disable()
		}
	}

	medialoop.OnChanged = func(b bool) {
		if b {
			s.Medialoop = true
		} else {
			s.Medialoop = false
		}
	}

	nextmedia.OnChanged = func(b bool) {
		if b {
			s.NextMedia = true
		} else {
			s.NextMedia = false
		}
	}

	// Device list auto-refresh
	go func() {
		for range refreshDevices.C {
			data2, _ := getDevices(2)
			data = data2
			list.Refresh()
		}
	}()

	go func() {
		var checkMuteCounter int
		for range checkMute.C {

			// Stop trying after 5 failures
			// to get the mute status
			if checkMuteCounter == 5 {
				s.renderingControlURL = ""
				checkMuteCounter = 0
			}

			if s.renderingControlURL == "" {
				continue
			}

			if s.tvdata == nil {
				s.tvdata = &soapcalls.TVPayload{RenderingControlURL: s.renderingControlURL}
			}

			isMuted, err := s.tvdata.GetMuteSoapCall()
			if err != nil {
				checkMuteCounter++
				continue
			}

			checkMuteCounter = 0

			switch isMuted {
			case "1":
				mute.Hide()
				unmute.Show()
			case "0":
				mute.Show()
				unmute.Hide()
			}
		}
	}()

	w.SetContent(content)
	w.Resize(fyne.NewSize(w.Canvas().Size().Width*1.4, w.Canvas().Size().Height*1.6))
	w.CenterOnScreen()
	w.ShowAndRun()
	os.Exit(0)
}

func muteAction(screen *NewScreen) {
	w := screen.Current
	if screen.renderingControlURL == "" {
		check(w, errors.New("please select a device"))
		return
	}
	if screen.tvdata == nil {
		// If tvdata is nil, we just need to set RenderingControlURL if we want
		// to control the sound. We should still rely on the play action to properly
		// populate our tvdata type.
		screen.tvdata = &soapcalls.TVPayload{RenderingControlURL: screen.renderingControlURL}
	}

	if err := screen.tvdata.SetMuteSoapCall("1"); err != nil {
		check(w, errors.New("could not send mute action"))
		return
	}
	screen.Unmute.Show()
	screen.Mute.Hide()
}

func unmuteAction(screen *NewScreen) {
	w := screen.Current
	if screen.renderingControlURL == "" {
		check(w, errors.New("please select a device"))
		return
	}
	if screen.tvdata == nil {
		// If tvdata is nil, we just need to set RenderingControlURL if we want
		// to control the sound. We should still rely on the play action to properly
		// populate our tvdata type.
		screen.tvdata = &soapcalls.TVPayload{RenderingControlURL: screen.renderingControlURL}
	}
	//isMuted, _ := screen.tvdata.GetMuteSoapCall()
	if err := screen.tvdata.SetMuteSoapCall("0"); err != nil {
		check(w, errors.New("could not send mute action"))
		return
	}
	screen.Unmute.Hide()
	screen.Mute.Show()
}

func mediaAction(screen *NewScreen) {
	w := screen.Current
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		check(w, err)

		if reader == nil {
			return
		}
		defer reader.Close()

		mfile := reader.URI().Path()
		absMediaFile, err := filepath.Abs(mfile)
		check(w, err)

		screen.MediaText.Text = filepath.Base(mfile)
		screen.mediafile = filestruct{
			abs:        absMediaFile,
			urlEncoded: utils.ConvertFilename(absMediaFile),
		}

		if !screen.CustomSubsCheck.Checked {
			selectSubs(absMediaFile, screen)
		}

		// Remember the last file location.
		screen.currentmfolder = filepath.Dir(absMediaFile)

		screen.MediaText.Refresh()
	}, w)

	fd.SetFilter(storage.NewExtensionFileFilter(screen.mediaFormats))

	if screen.currentmfolder != "" {
		mfileURI := storage.NewFileURI(screen.currentmfolder)
		mfileLister, err := storage.ListerForURI(mfileURI)
		check(w, err)
		fd.SetLocation(mfileLister)
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
		check(w, err)
		if err != nil {
			return
		}

		screen.SubsText.Text = filepath.Base(sfile)
		screen.subsfile = filestruct{
			abs:        absSubtitlesFile,
			urlEncoded: utils.ConvertFilename(absSubtitlesFile),
		}
		screen.SubsText.Refresh()
	}, w)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".srt"}))

	if screen.currentmfolder != "" {
		mfileURI := storage.NewFileURI(screen.currentmfolder)
		mfileLister, err := storage.ListerForURI(mfileURI)
		check(w, err)
		if err != nil {
			return
		}
		fd.SetLocation(mfileLister)
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
	if screen.mediafile.urlEncoded == "" {
		check(w, errors.New("please select a media file"))
		screen.Play.Enable()
		return
	}
	if screen.controlURL == "" {
		check(w, errors.New("please select a device"))
		screen.Play.Enable()
		return
	}

	if screen.tvdata == nil {
		stopAction(screen)
	}

	whereToListen, err := iptools.URLtoListenIPandPort(screen.controlURL)
	check(w, err)
	if err != nil {
		return
	}

	screen.tvdata = &soapcalls.TVPayload{
		ControlURL:          screen.controlURL,
		EventURL:            screen.eventlURL,
		RenderingControlURL: screen.renderingControlURL,
		MediaURL:            "http://" + whereToListen + "/" + screen.mediafile.urlEncoded,
		SubtitlesURL:        "http://" + whereToListen + "/" + screen.subsfile.urlEncoded,
		CallbackURL:         "http://" + whereToListen + "/callback",
		CurrentTimers:       make(map[string]*time.Timer),
	}

	screen.httpserver = httphandlers.NewServer(whereToListen)
	serverStarted := make(chan struct{})

	// We pass the tvdata here as we need the callback handlers to be able to react
	// to the different media renderer states.
	go func() {
		err := screen.httpserver.ServeFiles(serverStarted, screen.mediafile.abs, screen.subsfile.abs, screen.tvdata, screen)
		check(w, err)
		if err != nil {
			return
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
		screen.controlURL = ""
		stopAction(screen)
	}
}

func pauseAction(screen *NewScreen) {
	w := screen.Current
	screen.Pause.Disable()

	err := screen.tvdata.SendtoTV("Pause")
	check(w, err)
}

func clearmediaAction(screen *NewScreen) {
	screen.MediaText.Text = ""
	screen.mediafile.urlEncoded = ""
	screen.MediaText.Refresh()
}

func clearsubsAction(screen *NewScreen) {
	screen.SubsText.Text = ""
	screen.subsfile.urlEncoded = ""
	screen.SubsText.Refresh()
}

func stopAction(screen *NewScreen) {
	w := screen.Current

	screen.Play.Enable()
	screen.Pause.Enable()

	if screen.tvdata == nil || screen.tvdata.ControlURL == "" {
		return
	}
	err := screen.tvdata.SendtoTV("Stop")

	// Hack to avoid potential http errors during media loop mode.
	// Will keep the window clean during unattended usage.
	if screen.Medialoop {
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
		return nil, fmt.Errorf("getDevices error: %w", err)
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
func InitFyneNewScreen() *NewScreen {
	go2tv := app.New()
	app := go2tv.NewWindow("Go2TV")
	currentdir, err := os.Getwd()
	if err != nil {
		currentdir = ""
	}

	return &NewScreen{
		Current:        app,
		currentmfolder: currentdir,
		mediaFormats:   []string{".mp4", ".avi", ".mkv", ".mpeg", ".mov", ".webm", ".m4v", ".mpv", ".mp3"},
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
