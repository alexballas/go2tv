//go:build !(android || ios)
// +build !android,!ios

package gui

import (
	"context"
	"errors"
	"math"
	"net/url"
	"os/exec"
	"sort"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/alexballas/go2tv/devices"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/alexballas/go2tv/soapcalls/utils"
	"golang.org/x/time/rate"
)

type tappedSlider struct {
	mu sync.Mutex
	*widget.Slider
	screen *NewScreen
	end    string
}

func newTappableSlider(s *NewScreen) *tappedSlider {
	slider := &tappedSlider{
		Slider: &widget.Slider{
			Max: 100,
		},
		screen: s,
	}
	slider.ExtendBaseWidget(slider)
	return slider
}

func (t *tappedSlider) Dragged(e *fyne.DragEvent) {
	t.Slider.Dragged(e)
	t.screen.sliderActive = true

	if t.end == "" {
		getSliderPos, err := t.screen.tvdata.GetPositionInfo()
		if err != nil {
			return
		}

		t.mu.Lock()
		t.end = getSliderPos[0]
		t.mu.Unlock()

		// poor man's caching to reduce the amount of
		// GetPositionInfo calls.
		go func() {
			time.Sleep(time.Second)
			t.mu.Lock()
			t.end = ""
			t.mu.Unlock()
		}()
	}

	total, err := utils.ClockTimeToSeconds(t.end)
	if err != nil {
		return
	}

	cur := (float64(total) * t.Slider.Value) / t.Slider.Max
	roundedInt := int(math.Round(cur))

	reltime, err := utils.SecondsToClockTime(roundedInt)
	if err != nil {
		return
	}

	end, err := utils.FormatClockTime(t.end)
	if err != nil {
		return
	}

	t.screen.EndPos.Set(end)
	t.screen.CurrentPos.Set(reltime)
}

func (t *tappedSlider) DragEnd() {
	// This is a way to making the slider due to the DragEnd
	// action racing the auto-refresh action. The auto-refresh action
	// should reset this back to false after the first iterration.
	t.screen.sliderActive = true

	if t.screen.State == "Playing" || t.screen.State == "Paused" {
		getPos, err := t.screen.tvdata.GetPositionInfo()
		if err != nil {
			return
		}

		total, err := utils.ClockTimeToSeconds(getPos[0])
		if err != nil {
			return
		}

		cur := (float64(total) * t.screen.SlideBar.Value) / t.screen.SlideBar.Max
		roundedInt := int(math.Round(cur))

		reltime, err := utils.SecondsToClockTime(roundedInt)
		if err != nil {
			return
		}

		end, err := utils.FormatClockTime(getPos[0])
		if err != nil {
			return
		}

		t.screen.CurrentPos.Set(reltime)
		t.screen.EndPos.Set(end)

		if err := t.screen.tvdata.SeekSoapCall(reltime); err != nil {
			return
		}
	}
}

func (t *tappedSlider) Tapped(p *fyne.PointEvent) {
	// This is a way to making the slider due to the tapped
	// action racing the auto-refresh action. The auto-refresh action
	// should reset this back to false after the first iterration.
	t.screen.sliderActive = true

	// To remove this part when a new fyne version introduces t.Slider.Tapped(p)
	gaussian := func(x float64, mu float64, sigma float64) float64 {
		return math.Exp(-math.Pow(x-mu, 2) / (2 * math.Pow(sigma, 2)))
	}

	newpos := (p.Position.X / t.Size().Width) * float32(t.Max)
	padding := theme.Padding() + theme.InnerPadding()
	correction := (1 - gaussian(float64(newpos), 50, 20)) * float64(padding)

	switch {
	case newpos > 50:
		newpos = ((p.Position.X + float32(math.Ceil(correction))) / t.Size().Width) * float32(t.Max)
	case newpos < 50:
		newpos = ((p.Position.X - float32(math.Ceil(correction))) / t.Size().Width) * float32(t.Max)
	}

	if math.IsNaN(float64(newpos)) {
		return
	}

	t.SetValue(float64(newpos))

	if t.screen.State == "Playing" || t.screen.State == "Paused" {
		getPos, err := t.screen.tvdata.GetPositionInfo()
		if err != nil {
			return
		}

		total, err := utils.ClockTimeToSeconds(getPos[0])
		if err != nil {
			return
		}

		cur := (float64(total) * t.screen.SlideBar.Value) / t.screen.SlideBar.Max
		roundedInt := int(math.Round(cur))

		reltime, err := utils.SecondsToClockTime(roundedInt)
		if err != nil {
			return
		}

		end, err := utils.FormatClockTime(getPos[0])
		if err != nil {
			return
		}

		t.screen.CurrentPos.Set(reltime)
		t.screen.EndPos.Set(end)

		if err := t.screen.tvdata.SeekSoapCall(reltime); err != nil {
			return
		}
	}
}

func mainWindow(s *NewScreen) fyne.CanvasObject {
	w := s.Current
	list := new(widget.List)

	var data []devType

	w.Canvas().SetOnTypedKey(func(k *fyne.KeyEvent) {
		if !s.Hotkeys {
			return
		}

		fynePE := &fyne.PointEvent{
			AbsolutePosition: fyne.Position{
				X: 10,
				Y: 30,
			},
			Position: fyne.Position{
				X: 10,
				Y: 30,
			},
		}

		if k.Name == "Space" || k.Name == "P" {

			currentState := s.getScreenState()
			switch currentState {
			case "Playing":
				go s.PlayPause.Tapped(fynePE)
			case "Paused", "Stopped", "":
				go s.PlayPause.Tapped(fynePE)
			}
		}

		if k.Name == "S" {
			go s.Stop.Tapped(fynePE)
		}

		if k.Name == "M" {
			s.MuteUnmute.Tapped(fynePE)
		}

		if k.Name == "Prior" {
			s.VolumeUp.Tapped(fynePE)
		}

		if k.Name == "Next" {
			s.VolumeDown.Tapped(fynePE)
		}
	})

	go func() {
		datanew, err := getDevices(1)
		data = datanew
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

	sfile := widget.NewButton("Select Subtitles File", func() {
		go subsAction(s)
	})

	sfile.Disable()
	sfiletext.Disable()

	playpause := widget.NewButtonWithIcon("Play", theme.MediaPlayIcon(), func() {
		go playAction(s)
	})

	stop := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), func() {
		go stopAction(s)
	})

	volumeup := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		go volumeAction(s, true)
	})

	muteunmute := widget.NewButtonWithIcon("", theme.VolumeMuteIcon(), func() {
		go muteAction(s)
	})

	volumedown := widget.NewButtonWithIcon("", theme.ContentRemoveIcon(), func() {
		go volumeAction(s, false)
	})

	clearmedia := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		go clearmediaAction(s)
	})

	clearsubs := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		go clearsubsAction(s)
	})

	skipNext := widget.NewButtonWithIcon("", theme.MediaSkipNextIcon(), func() {
		go skipNextAction(s)
	})

	sliderBar := newTappableSlider(s)

	// previewmedia spawns external applications.
	// Since there is no way to monitor the time it takes
	// for the apps to load, we introduce a rate limit
	// for the specific action.
	throttle := rate.Every(3 * time.Second)
	r := rate.NewLimiter(throttle, 1)
	previewmedia := widget.NewButtonWithIcon("", theme.VisibilityIcon(), func() {
		if !r.Allow() {
			return
		}
		go previewmedia(s)
	})

	sfilecheck := widget.NewCheck("Custom Subtitles", func(b bool) {})
	externalmedia := widget.NewCheck("Media from URL", func(b bool) {})
	medialoop := widget.NewCheck("Loop Selected", func(b bool) {})
	nextmedia := widget.NewCheck("Auto-Play Next File", func(b bool) {})
	transcode := widget.NewCheck("Transcode", func(b bool) {})

	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		transcode.Disable()
	}

	mediafilelabel := canvas.NewText("File:", nil)
	subsfilelabel := canvas.NewText("Subtitles:", nil)
	devicelabel := canvas.NewText("Select Device:", nil)

	var intListCont fyne.CanvasObject
	list = widget.NewList(
		func() int {
			return len(data)
		},
		func() fyne.CanvasObject {
			intListCont = container.NewHBox(widget.NewIcon(theme.NavigateNextIcon()), widget.NewLabel(""))
			return intListCont
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*fyne.Container).Objects[1].(*widget.Label).SetText(data[i].name)
		})

	curPos := binding.NewString()
	endPos := binding.NewString()

	s.PlayPause = playpause
	s.Stop = stop
	s.MuteUnmute = muteunmute
	s.CustomSubsCheck = sfilecheck
	s.ExternalMediaURL = externalmedia
	s.MediaText = mfiletext
	s.SubsText = sfiletext
	s.DeviceList = list
	s.VolumeUp = volumeup
	s.VolumeDown = volumedown
	s.NextMediaCheck = nextmedia
	s.SlideBar = sliderBar
	s.CurrentPos = curPos
	s.EndPos = endPos

	curPos.Set("00:00:00")
	endPos.Set("00:00:00")

	sliderArea := container.NewBorder(nil, nil, widget.NewLabelWithData(curPos), widget.NewLabelWithData(endPos), sliderBar)
	actionbuttons := container.New(&mainButtonsLayout{buttonHeight: 1.0, buttonPadding: theme.Padding()},
		playpause,
		volumedown,
		muteunmute,
		volumeup,
		stop)

	mrightbuttons := container.NewHBox(skipNext, previewmedia, clearmedia)

	checklists := container.NewHBox(externalmedia, sfilecheck, medialoop, nextmedia, transcode)
	mediasubsbuttons := container.New(layout.NewGridLayout(2), mfile, sfile)
	mfiletextArea := container.New(layout.NewBorderLayout(nil, nil, nil, mrightbuttons), mrightbuttons, mfiletext)
	sfiletextArea := container.New(layout.NewBorderLayout(nil, nil, nil, clearsubs), clearsubs, sfiletext)
	viewfilescont := container.New(layout.NewFormLayout(), mediafilelabel, mfiletextArea, subsfilelabel, sfiletextArea)
	buttons := container.NewVBox(mediasubsbuttons, viewfilescont, checklists, sliderArea, actionbuttons, container.NewPadded(devicelabel))
	content := container.New(layout.NewBorderLayout(buttons, nil, nil, nil), buttons, list)

	// Widgets actions
	list.OnSelected = func(id widget.ListItemID) {
		playpause.Enable()
		t, err := soapcalls.DMRextractor(context.Background(), data[id].addr)
		check(s, err)
		if err == nil {
			s.selectedDevice = data[id]
			s.controlURL = t.AvtransportControlURL
			s.eventlURL = t.AvtransportEventSubURL
			s.renderingControlURL = t.RenderingControlURL
			s.connectionManagerURL = t.ConnectionManagerURL
			if s.tvdata != nil {
				s.tvdata.RenderingControlURL = s.renderingControlURL
			}
		}
	}

	transcode.OnChanged = func(b bool) {
		if b {
			s.Transcode = true
			return
		}

		s.Transcode = false
	}

	sfilecheck.OnChanged = func(b bool) {
		if b {
			sfile.Enable()
			return
		}

		sfile.Disable()
	}

	var mediafileOld, mediafileOldText string

	externalmedia.OnChanged = func(b bool) {
		if b {
			nextmedia.SetChecked(false)
			nextmedia.Disable()
			mfile.Disable()
			previewmedia.Disable()

			// keep old values
			mediafileOld = s.mediafile
			mediafileOldText = s.MediaText.Text

			// rename the label
			mediafilelabel.Text = "URL:"
			mediafilelabel.Refresh()

			// Clear the Media Text Area
			clearmediaAction(s)

			// Set some Media text defaults
			// to indicate that we're expecting a URL
			s.MediaText.SetPlaceHolder("Enter URL here")
			s.MediaText.Enable()
			return
		}

		if !nextmedia.Checked {
			medialoop.Enable()
		}

		if !medialoop.Checked {
			nextmedia.Enable()
		}

		mfile.Enable()
		previewmedia.Enable()
		mediafilelabel.Text = "File:"
		s.MediaText.SetPlaceHolder("")
		s.MediaText.Text = mediafileOldText
		s.mediafile = mediafileOld
		mediafilelabel.Refresh()
		s.MediaText.Disable()
	}

	medialoop.OnChanged = func(b bool) {
		s.Medialoop = b
		if b {
			nextmedia.SetChecked(false)
			nextmedia.Disable()
			return
		}

		if !externalmedia.Checked {
			nextmedia.Enable()
		}
	}

	nextmedia.OnChanged = func(b bool) {
		switch b {
		case true:
			medialoop.SetChecked(false)
			medialoop.Disable()
		case false:
			medialoop.Enable()
		}

		go func() {
			gaplessOption := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")

			if b {
				if gaplessOption == "Enabled" {
					switch s.State {
					case "Playing", "Paused":
						newTVPayload, err := queueNext(s, false)
						if err == nil && s.GaplessMediaWatcher == nil {
							s.GaplessMediaWatcher = gaplessMediaWatcher
							go s.GaplessMediaWatcher(s.serverStopCTX, s, newTVPayload)
						}
					}
				}
				return
			}

			if s.tvdata != nil && s.tvdata.CallbackURL != "" {
				_, err = queueNext(s, true)
				if err != nil {
					stopAction(s)
				}
			}
		}()
	}

	// Device list auto-refresh.
	// TODO: Add context to cancel
	go refreshDevList(s, &data)

	// Check mute status for selected device.
	// TODO: Add context to cancel
	go checkMutefunc(s)

	// Keep track of the media progress and reflect that to the slide bar.
	// TODO: Add context to cancel
	go sliderUpdate(s)
	return content
}

func refreshDevList(s *NewScreen, data *[]devType) {
	refreshDevices := time.NewTicker(5 * time.Second)

	_, err := getDevices(2)
	if err != nil && !errors.Is(err, devices.ErrNoDeviceAvailable) {
		check(s, err)
	}

	for range refreshDevices.C {
		datanew, _ := getDevices(2)

	outer:
		for _, old := range *data {
			oldAddress, _ := url.Parse(old.addr)
			for _, new := range datanew {
				newAddress, _ := url.Parse(new.addr)
				if newAddress.Host == oldAddress.Host {
					continue outer
				}
			}

			if utils.HostPortIsAlive(oldAddress.Host) {
				datanew = append(datanew, old)
			}

			sort.Slice(datanew, func(i, j int) bool {
				return (datanew)[i].name < (datanew)[j].name
			})
		}

		// check to see if the new refresh includes
		// one of the already selected devices
		var includes bool
		u, _ := url.Parse(s.controlURL)
		for _, d := range datanew {
			n, _ := url.Parse(d.addr)
			if n.Host == u.Host {
				includes = true
			}
		}

		*data = datanew

		if !includes && !utils.HostPortIsAlive(u.Host) {
			s.controlURL = ""
			s.DeviceList.UnselectAll()
		}

		var found bool
		for n, a := range *data {
			if s.selectedDevice.addr == a.addr {
				found = true
				s.DeviceList.Select(n)
			}
		}

		if !found {
			s.DeviceList.UnselectAll()
		}

		s.DeviceList.Refresh()
	}
}

func checkMutefunc(s *NewScreen) {
	checkMute := time.NewTicker(2 * time.Second)

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
			setMuteUnmuteView("Unmute", s)
		case "0":
			setMuteUnmuteView("Mute", s)
		}
	}
}

func sliderUpdate(s *NewScreen) {
	t := time.NewTicker(time.Second)
	for range t.C {
		if s.sliderActive {
			s.sliderActive = false
			continue
		}

		if s.State == "Stopped" || s.State == "" {
			s.SlideBar.Slider.SetValue(0)
			s.CurrentPos.Set("00:00:00")
			s.EndPos.Set("00:00:00")
		}

		if s.State == "Playing" {
			getPos, err := s.tvdata.GetPositionInfo()
			if err != nil {
				continue
			}

			total, err := utils.ClockTimeToSeconds(getPos[0])
			if err != nil {
				continue
			}

			current, err := utils.ClockTimeToSeconds(getPos[1])
			if err != nil {
				continue
			}

			valueToSet := float64(current) * s.SlideBar.Max / float64(total)
			if !math.IsNaN(valueToSet) {
				s.SlideBar.SetValue(valueToSet)

				end, err := utils.FormatClockTime(getPos[0])
				if err != nil {
					return
				}

				current, err := utils.FormatClockTime(getPos[1])
				if err != nil {
					return
				}

				s.CurrentPos.Set(current)
				s.EndPos.Set(end)
			}
		}
	}
}
