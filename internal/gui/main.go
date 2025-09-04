//go:build !(android || ios)
// +build !android,!ios

package gui

import (
	"context"
	"errors"
	"math"
	"net/url"
	"sort"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/alexballas/go2tv/devices"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/alexballas/go2tv/soapcalls/utils"
	"golang.org/x/time/rate"
)

type tappedSlider struct {
	*widget.Slider
	screen *FyneScreen
	end    string
	mu     sync.Mutex
}

type deviceList struct {
	widget.List
}

func (c *deviceList) FocusGained() {}

func newDeviceList(dd *[]devType) *deviceList {
	list := &deviceList{}

	list.Length = func() int {
		return len(*dd)
	}

	list.CreateItem = func() fyne.CanvasObject {
		intListCont := container.NewHBox(widget.NewIcon(theme.NavigateNextIcon()), widget.NewLabel(""))
		return intListCont
	}

	list.UpdateItem = func(i widget.ListItemID, o fyne.CanvasObject) {
		fyne.Do(func() {
			o.(*fyne.Container).Objects[1].(*widget.Label).SetText((*dd)[i].name)
		})
	}

	list.ExtendBaseWidget(list)
	return list
}

func newTappableSlider(s *FyneScreen) *tappedSlider {
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
	// This ensures the slider functions correctly by addressing the race condition
	// between the DragEnd action and the auto-refresh action.
	// The auto-refresh action will reset this flag to false after the first iteration.
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

		if t.screen.tvdata.Transcode {
			t.screen.ffmpegSeek = roundedInt
			stopAction(t.screen)
			playAction(t.screen)
		}

		if err := t.screen.tvdata.SeekSoapCall(reltime); err != nil {
			return
		}
	}
}

func (t *tappedSlider) Tapped(p *fyne.PointEvent) {
	// The auto-refresh action should reset this back to false
	// after the first iterration.
	t.screen.sliderActive = true

	t.Slider.Tapped(p)

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

		if t.screen.tvdata.Transcode {
			t.screen.ffmpegSeek = roundedInt
			stopAction(t.screen)
			playAction(t.screen)
		}

		if err := t.screen.tvdata.SeekSoapCall(reltime); err != nil {
			return
		}
	}
}

func mainWindow(s *FyneScreen) fyne.CanvasObject {
	w := s.Current
	var data []devType
	list := newDeviceList(&data)

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

	w.Canvas().SetOnTypedKey(func(k *fyne.KeyEvent) {
		if !s.Hotkeys {
			return
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

	// Avoid parallel execution of getDevices.
	blockGetDevices := make(chan struct{})
	go func() {
		var err error
		data, err = getDevices(1)
		if err != nil {
			data = nil
		}

		sort.Slice(data, func(i, j int) bool {
			return (data)[i].name < (data)[j].name
		})

		fyne.Do(func() {
			list.Refresh()
		})

		blockGetDevices <- struct{}{}
	}()

	mfiletext := widget.NewEntry()
	sfiletext := widget.NewEntry()

	mfile := widget.NewButton(lang.L("Select Media File"), func() {
		mediaAction(s)
	})

	mfiletext.Disable()

	sfile := widget.NewButton(lang.L("Select Subtitles File"), func() {
		subsAction(s)
	})

	sfile.Disable()
	sfiletext.Disable()

	playpause := widget.NewButtonWithIcon(lang.L("Play"), theme.MediaPlayIcon(), func() {
		go fyne.Do(func() {
			playAction(s)
		})
	})

	stop := widget.NewButtonWithIcon(lang.L("Stop"), theme.MediaStopIcon(), func() {
		go fyne.Do(func() {
			stopAction(s)
		})
	})

	volumeup := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		go fyne.Do(func() {
			volumeAction(s, true)
		})
	})

	muteunmute := widget.NewButtonWithIcon("", theme.VolumeUpIcon(), func() {
		go fyne.Do(func() {
			muteAction(s)
		})
	})

	volumedown := widget.NewButtonWithIcon("", theme.ContentRemoveIcon(), func() {
		go fyne.Do(func() {
			volumeAction(s, false)
		})
	})

	clearmedia := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		go fyne.Do(func() {
			clearmediaAction(s)
		})
	})

	clearsubs := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		go fyne.Do(func() {
			clearsubsAction(s)
		})
	})

	skipNext := widget.NewButtonWithIcon("", theme.MediaSkipNextIcon(), func() {
		go fyne.Do(func() {
			skipNextAction(s)
		})
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
		go fyne.Do(func() {
			previewmedia(s)
		})
	})

	sfilecheck := widget.NewCheck(lang.L("Manual Subtitles"), func(b bool) {})
	externalmedia := widget.NewCheck(lang.L("Media from URL"), func(b bool) {})
	medialoop := widget.NewCheck(lang.L("Loop Selected"), func(b bool) {})
	nextmedia := widget.NewCheck(lang.L("Auto-Play Next File"), func(b bool) {})
	transcode := widget.NewCheck(lang.L("Transcode"), func(b bool) {})

	mediafilelabel := widget.NewLabel(lang.L("File") + ":")
	subsfilelabel := widget.NewLabel(lang.L("Subtitles") + ":")
	devicelabel := widget.NewLabel(lang.L("Select Device") + ":")

	selectInternalSubs := widget.NewSelect([]string{}, func(item string) {
		if item == "" {
			return
		}
		s.SubsText.Text = ""
		s.subsfile = ""
		s.SubsText.Refresh()
		sfilecheck.Checked = false
		sfilecheck.Refresh()
		sfile.Disable()
	})

	selectInternalSubs.PlaceHolder = lang.L("No Embedded Subs")
	selectInternalSubs.Disable()

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
	s.SelectInternalSubs = selectInternalSubs
	s.TranscodeCheckBox = transcode

	curPos.Set("00:00:00")
	endPos.Set("00:00:00")

	sliderArea := container.NewBorder(nil, nil, widget.NewLabelWithData(curPos), widget.NewLabelWithData(endPos), sliderBar)
	actionbuttons := container.New(&mainButtonsLayout{buttonHeight: 1.0, buttonPadding: theme.Padding()},
		playpause,
		volumedown,
		muteunmute,
		volumeup,
		stop)

	mrightwidgets := container.NewHBox(skipNext, previewmedia, clearmedia)
	srightwidgets := container.NewHBox(selectInternalSubs, clearsubs)

	checklists := container.NewHBox(externalmedia, sfilecheck, medialoop, nextmedia, transcode)
	mediasubsbuttons := container.New(layout.NewGridLayout(2), mfile, sfile)
	mfiletextArea := container.New(layout.NewBorderLayout(nil, nil, nil, mrightwidgets), mrightwidgets, mfiletext)
	sfiletextArea := container.New(layout.NewBorderLayout(nil, nil, nil, srightwidgets), srightwidgets, sfiletext)
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
			skipNext.Disable()

			// keep old values
			mediafileOld = s.mediafile
			mediafileOldText = s.MediaText.Text

			// rename the label
			mediafilelabel.Text = lang.L("URL") + ":"
			mediafilelabel.Refresh()

			// Clear the Media Text Area
			clearmediaAction(s)

			// Set some Media text defaults
			// to indicate that we're expecting a URL
			s.MediaText.SetPlaceHolder(lang.L("Enter URL here"))
			s.MediaText.Enable()

			s.SelectInternalSubs.PlaceHolder = lang.L("No Embedded Subs")
			s.SelectInternalSubs.ClearSelected()
			s.SelectInternalSubs.Disable()
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
		skipNext.Enable()
		mediafilelabel.Text = lang.L("File") + ":"
		s.MediaText.SetPlaceHolder("")
		s.MediaText.Text = mediafileOldText
		s.mediafile = mediafileOld
		mediafilelabel.Refresh()
		s.MediaText.Disable()

		if mediafileOld != "" {
			subs, err := utils.GetSubs(s.ffmpegPath, mediafileOld)
			if err != nil {
				s.SelectInternalSubs.Options = []string{}
				s.SelectInternalSubs.PlaceHolder = lang.L("No Embedded Subs")
				s.SelectInternalSubs.ClearSelected()
				s.SelectInternalSubs.Disable()
				return
			}

			s.SelectInternalSubs.PlaceHolder = lang.L("Embedded Subs")
			s.SelectInternalSubs.Options = subs
			s.SelectInternalSubs.Enable()
		}
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
				_, err := queueNext(s, true)
				if err != nil {
					stopAction(s)
				}
			}
		}()
	}

	// Device list auto-refresh.
	// TODO: Add context to cancel
	go func() {
		<-blockGetDevices
		refreshDevList(s, &data)
	}()

	// Check mute status for selected device.
	// TODO: Add context to cancel
	go checkMutefunc(s)

	// Keep track of the media progress and reflect that to the slide bar.
	// TODO: Add context to cancel
	go sliderUpdate(s)
	return content
}

func refreshDevList(s *FyneScreen, data *[]devType) {
	refreshDevices := time.NewTicker(5 * time.Second)

	_, err := getDevices(2)
	if err != nil && !errors.Is(err, devices.ErrNoDeviceAvailable) {
		check(s, err)
	}

	for range refreshDevices.C {
		newDevices, _ := getDevices(2)

	outer:
		for _, old := range *data {
			oldAddress, _ := url.Parse(old.addr)
			for _, device := range newDevices {
				newAddress, _ := url.Parse(device.addr)
				if newAddress.Host == oldAddress.Host {
					continue outer
				}
			}

			if utils.HostPortIsAlive(oldAddress.Host) {
				newDevices = append(newDevices, old)
			}

			sort.Slice(newDevices, func(i, j int) bool {
				return (newDevices)[i].name < (newDevices)[j].name
			})
		}

		// check to see if the new refresh includes
		// one of the already selected devices
		var includes bool
		u, _ := url.Parse(s.controlURL)
		for _, d := range newDevices {
			n, _ := url.Parse(d.addr)
			if n.Host == u.Host {
				includes = true
			}
		}

		*data = newDevices

		if !includes && !utils.HostPortIsAlive(u.Host) {
			s.controlURL = ""
			fyne.Do(func() {
				s.DeviceList.UnselectAll()
			})
		}

		var found bool
		for n, a := range *data {
			if s.selectedDevice.addr == a.addr {
				found = true
				fyne.Do(func() {
					s.DeviceList.Select(n)
				})
			}
		}

		if !found {
			fyne.Do(func() {
				s.DeviceList.UnselectAll()
			})
		}

		fyne.Do(func() {
			s.DeviceList.Refresh()
		})
	}
}

func checkMutefunc(s *FyneScreen) {
	checkMute := time.NewTicker(2 * time.Second)

	var checkMuteCounter int
	for range checkMute.C {
		// Stop trying to get the mute status after 5 failures.
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

func sliderUpdate(s *FyneScreen) {
	t := time.NewTicker(time.Second)
	for range t.C {
		if s.sliderActive {
			s.sliderActive = false
			continue
		}

		if (s.State == "Stopped" || s.State == "") && s.ffmpegSeek == 0 {
			fyne.Do(func() {
				s.SlideBar.Slider.SetValue(0)
				s.CurrentPos.Set("00:00:00")
				s.EndPos.Set("00:00:00")
			})
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

			switch {
			case s.ffmpegSeek > 0:
				current += s.ffmpegSeek
			case s.tvdata != nil && s.tvdata.FFmpegSeek > 0:
				current += s.tvdata.FFmpegSeek
			}

			fyne.Do(func() {
				s.ffmpegSeek = 0
			})

			valueToSet := float64(current) * s.SlideBar.Max / float64(total)
			if !math.IsNaN(valueToSet) {
				fyne.Do(func() {
					s.SlideBar.SetValue(valueToSet)
				})

				end, err := utils.FormatClockTime(getPos[0])
				if err != nil {
					return
				}

				currentClock, err := utils.SecondsToClockTime(current)
				if err != nil {
					return
				}

				s.CurrentPos.Set(currentClock)
				s.EndPos.Set(end)
			}
		}
	}
}
