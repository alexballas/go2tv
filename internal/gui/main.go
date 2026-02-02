//go:build !(android || ios)

package gui

import (
	"context"
	"errors"
	"math"
	"net/url"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
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

func newDeviceList(s *FyneScreen, dd *[]devType) *deviceList {
	list := &deviceList{}

	list.Length = func() int {
		return len(*dd)
	}

	list.CreateItem = func() fyne.CanvasObject {
		label := widget.NewLabel("Device Name")

		// Persistent icon for the device (Left side)
		// This will be swapped to a Play icon when active
		icon := widget.NewIcon(castIcon())

		return container.NewBorder(nil, nil, icon, nil, label)
	}

	list.UpdateItem = func(i widget.ListItemID, o fyne.CanvasObject) {
		container := o.(*fyne.Container)

		var navIcon *widget.Icon
		var txtLabel *widget.Label

		for _, obj := range container.Objects {
			if l, ok := obj.(*widget.Label); ok {
				txtLabel = l
			}
			// Standalone icon is the main indicator (Left)
			if icon, ok := obj.(*widget.Icon); ok {
				navIcon = icon
			}
		}

		item := (*dd)[i]

		if txtLabel != nil {
			txtLabel.SetText(item.name)
		}

		// Determine if this device is active
		isActive := false
		currentState := s.getScreenState()
		isActivePlayback := currentState == "Playing" || currentState == "Paused"

		if isActivePlayback {
			// Prioritize Chromecast if connected
			// The app logic generally effectively locks to one active session type
			if s.chromecastClient != nil && s.chromecastClient.IsConnected() {
				// Check Chromecast
				if item.deviceType == devices.DeviceTypeChromecast {

					// Chromecast items in list are URLs "http://host:port"
					// s.chromecastClient.Host() returns just host (IP/hostname)

					// Parse URL using net/url
					u, err := url.Parse(item.addr)
					if err == nil {
						if u.Hostname() == s.chromecastClient.Host() {
							isActive = true
						}
					}
				}
			} else if s.tvdata != nil {
				// Fallback to DLNA if Chromecast is not active
				// Check DLNA
				if item.deviceType == devices.DeviceTypeDLNA {
					// Parse ControlURL to get host
					u, err := url.Parse(s.tvdata.ControlURL)

					if err == nil {
						// Parse item address
						itemURL, err2 := url.Parse(item.addr)
						if err2 == nil && u.Host == itemURL.Host {
							isActive = true
						}
					}
				}
			}
		}

		// Swap icon based on state
		if isActive {
			if navIcon != nil {
				navIcon.SetResource(theme.MediaPlayIcon())
				navIcon.Refresh()
			}
		} else {
			if navIcon != nil {
				navIcon.SetResource(castIcon())
				navIcon.Refresh()
			}
		}
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

	// Handle Chromecast with stored duration (transcoded streams)
	if t.screen.mediaDuration > 0 {
		cur := (t.screen.mediaDuration * t.Slider.Value) / t.Slider.Max
		reltime, _ := utils.SecondsToClockTime(int(cur))
		total, _ := utils.SecondsToClockTime(int(t.screen.mediaDuration))
		t.screen.CurrentPos.Set(reltime)
		t.screen.EndPos.Set(total)
		return
	}

	// DLNA: Get position from device
	if t.end == "" {
		if t.screen.tvdata == nil {
			return
		}
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
		// Handle Chromecast seeking
		if t.screen.chromecastClient != nil && t.screen.chromecastClient.IsConnected() {
			// For transcoded streams, use stored duration (Chromecast only knows buffered duration)
			var duration float64
			if t.screen.mediaDuration > 0 {
				duration = t.screen.mediaDuration
			} else {
				status, err := t.screen.chromecastClient.GetStatus()
				if err != nil || status.Duration <= 0 {
					return
				}
				duration = float64(status.Duration)
			}
			seekPos := int((t.screen.SlideBar.Value / t.screen.SlideBar.Max) * duration)
			// Transcoded seek: use optimized helper that keeps connection open
			// (Chromecast's native Seek() doesn't work on transcoded streams)
			if t.screen.mediaDuration > 0 {
				chromecastTranscodedSeek(t.screen, seekPos)
				return
			}
			// Non-transcoded seek: use Chromecast's native seek
			if err := t.screen.chromecastClient.Seek(seekPos); err != nil {
				return
			}
			return
		}

		// Handle DLNA seeking
		if t.screen.tvdata == nil {
			return
		}
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
		// Handle Chromecast seeking
		if t.screen.chromecastClient != nil && t.screen.chromecastClient.IsConnected() {
			// For transcoded streams, use stored duration (Chromecast only knows buffered duration)
			var duration float64
			if t.screen.mediaDuration > 0 {
				duration = t.screen.mediaDuration
			} else {
				status, err := t.screen.chromecastClient.GetStatus()
				if err != nil || status.Duration <= 0 {
					return
				}
				duration = float64(status.Duration)
			}

			seekPos := int((t.screen.SlideBar.Value / t.screen.SlideBar.Max) * duration)

			// Update time labels immediately for visual feedback (like DLNA)
			current, _ := utils.SecondsToClockTime(seekPos)
			total, _ := utils.SecondsToClockTime(int(duration))
			t.screen.CurrentPos.Set(current)
			t.screen.EndPos.Set(total)

			// Transcoded seek: use optimized helper that keeps connection open
			if t.screen.mediaDuration > 0 {
				chromecastTranscodedSeek(t.screen, seekPos)
				return
			}

			// Non-transcoded seek: use Chromecast's native seek
			if err := t.screen.chromecastClient.Seek(seekPos); err != nil {
				return
			}
			return
		}

		// Handle DLNA seeking
		if t.screen.tvdata == nil {
			return
		}
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
	list := newDeviceList(s, &data)

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

		if k.Name == "N" {
			s.SkipNextButton.Tapped(fynePE)
		}
	})

	// Avoid parallel execution of getDevices.
	blockGetDevices := make(chan struct{})
	go func() {
		datanew, err := getDevices(1)
		if err != nil {
			datanew = nil
		}

		fyne.DoAndWait(func() {
			data = datanew
			list.Refresh()
		})

		blockGetDevices <- struct{}{}
	}()

	mfiletext := widget.NewEntry()
	sfiletext := widget.NewEntry()

	mbrowse := widget.NewButtonWithIcon(lang.L("Browse"), theme.FolderOpenIcon(), func() {
		mediaAction(s)
	})

	mfiletext.Disable()

	sbrowse := widget.NewButtonWithIcon(lang.L("Browse"), theme.FolderOpenIcon(), func() {
		subsAction(s)
	})

	sbrowse.Disable()
	sfiletext.Disable()

	playpause := widget.NewButtonWithIcon(lang.L("Play"), theme.MediaPlayIcon(), func() {
		playAction(s)
	})

	stop := widget.NewButtonWithIcon(lang.L("Stop"), theme.MediaStopIcon(), func() {
		stopAction(s)
	})

	volumeup := widget.NewButtonWithIcon("", theme.VolumeUpIcon(), func() {
		volumeAction(s, true)
	})

	muteunmute := widget.NewButtonWithIcon("", theme.VolumeMuteIcon(), func() {
		muteAction(s)
	})

	volumedown := widget.NewButtonWithIcon("", theme.VolumeDownIcon(), func() {
		volumeAction(s, false)
	})

	clearmedia := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		clearmediaAction(s)
	})

	clearsubs := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		clearsubsAction(s)
	})

	skipNext := widget.NewButtonWithIcon("Next", theme.MediaSkipNextIcon(), func() {
		skipNextAction(s)
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

	sfilecheck := widget.NewCheck(lang.L("Manual Subtitles"), func(b bool) {})
	externalmedia := widget.NewCheck(lang.L("Media from URL"), func(b bool) {})
	medialoop := widget.NewCheck(lang.L("Loop Selected"), func(b bool) {})
	nextmedia := widget.NewCheck(lang.L("Auto-Play Next File"), func(b bool) {})
	transcode := widget.NewCheck(lang.L("Transcode"), func(b bool) {})
	rtmpServerCheck := widget.NewCheck(lang.L("Enable RTMP Server"), func(b bool) {
		if b {
			startRTMPServer(s)
		} else {
			stopRTMPServer(s)
		}
	})
	s.rtmpServerCheck = rtmpServerCheck

	s.rtmpURLEntry = widget.NewEntry()
	s.rtmpURLEntry.Disable()
	copyURLBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
		w.Clipboard().SetContent(s.rtmpURLEntry.Text)
	})

	s.rtmpKeyEntry = widget.NewEntry()
	s.rtmpKeyEntry.Password = true
	s.rtmpKeyEntry.Disable()
	copyKeyBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
		w.Clipboard().SetContent(s.rtmpKeyEntry.Text)
	})

	var toggleKeyBtn *widget.Button
	toggleKeyBtn = widget.NewButtonWithIcon("", theme.VisibilityIcon(), func() {
		if s.rtmpKeyEntry.Password {
			s.rtmpKeyEntry.Password = false
			toggleKeyBtn.SetIcon(theme.VisibilityOffIcon())
		} else {
			s.rtmpKeyEntry.Password = true
			toggleKeyBtn.SetIcon(theme.VisibilityIcon())
		}
		s.rtmpKeyEntry.Refresh()
	})

	rtmpRows := container.NewVBox(
		container.NewVBox(
			widget.NewLabelWithStyle(lang.L("RTMP Stream URL"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewBorder(nil, nil, nil, copyURLBtn, s.rtmpURLEntry),
		),
		container.NewVBox(
			widget.NewLabelWithStyle(lang.L("Stream Key"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			container.NewBorder(nil, nil, nil, container.NewHBox(toggleKeyBtn, copyKeyBtn), s.rtmpKeyEntry),
		),
	)

	s.rtmpURLCard = widget.NewCard(lang.L("RTMP Server"), "", rtmpRows)
	s.rtmpURLCard.Hide()

	mediafilelabel := widget.NewLabel(lang.L("Media File") + ":")
	subsfilelabel := widget.NewLabel(lang.L("Subtitles") + ":")

	selectInternalSubs := widget.NewSelect([]string{}, func(item string) {
		if item == "" {
			return
		}
		s.SubsText.Text = ""
		s.subsfile = ""
		s.SubsText.Refresh()
		sfilecheck.Checked = false
		sfilecheck.Refresh()
		s.SubsBrowse.Disable()
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
	s.SkipNextButton = skipNext
	s.SlideBar = sliderBar
	s.CurrentPos = curPos
	s.EndPos = endPos
	s.SelectInternalSubs = selectInternalSubs
	s.TranscodeCheckBox = transcode
	s.LoopSelectedCheck = medialoop
	s.MediaBrowse = mbrowse
	s.ClearMedia = clearmedia
	s.SubsBrowse = sbrowse

	curPos.Set("00:00:00")
	endPos.Set("00:00:00")

	sliderArea := container.NewBorder(nil, nil, widget.NewLabelWithData(curPos), widget.NewLabelWithData(endPos), sliderBar)
	actionbuttons := container.NewHBox(
		playpause,
		stop,
		skipNext,
		layout.NewSpacer(),
		volumedown,
		muteunmute,
		volumeup)

	mrightwidgets := container.NewHBox(previewmedia, clearmedia, mbrowse)
	srightwidgets := container.NewHBox(selectInternalSubs, clearsubs, sbrowse)

	mfiletextArea := container.New(layout.NewBorderLayout(nil, nil, nil, mrightwidgets), mrightwidgets, mfiletext)
	sfiletextArea := container.New(layout.NewBorderLayout(nil, nil, nil, srightwidgets), srightwidgets, sfiletext)
	viewfilescont := container.New(layout.NewFormLayout(), mediafilelabel, mfiletextArea, subsfilelabel, sfiletextArea)

	mediaCard := widget.NewCard(lang.L("Media"), "", viewfilescont)

	commonCard := widget.NewCard(lang.L("Common Options"), "", container.NewVBox(medialoop, nextmedia))
	advancedCard := widget.NewCard(lang.L("Advanced Options"), "", container.NewVBox(externalmedia, sfilecheck, transcode, rtmpServerCheck))

	playCard := widget.NewCard(lang.L("Playback"), "", container.NewVBox(sliderArea, actionbuttons))

	deviceHeader := widget.NewLabelWithStyle(lang.L("(auto refreshing)"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	s.ActiveDeviceLabel = widget.NewLabel("")
	s.ActiveDeviceCard = widget.NewCard(lang.L("Active Device"), "",
		container.NewHBox(widget.NewIcon(theme.MediaPlayIcon()), s.ActiveDeviceLabel))
	s.ActiveDeviceCard.Hide()

	deviceBottom := container.NewVBox(s.ActiveDeviceCard, s.rtmpURLCard)
	deviceCard := widget.NewCard(lang.L("Devices"), "", container.NewBorder(deviceHeader, deviceBottom, nil, nil, list))

	leftColumn := container.NewVBox(mediaCard, playCard, commonCard, advancedCard)
	content := container.New(&RatioLayout{LeftRatio: 0.66}, leftColumn, deviceCard)

	// Widgets actions
	list.OnSelected = func(id widget.ListItemID) {
		// Only reset DLNA-specific state when switching devices, NOT Chromecast playback.
		// This allows browsing the device list while Chromecast is playing.
		// Chromecast connection is only closed via stop button or when starting new playback.
		// Also don't reset tvdata if something is currently playing - user should be able
		// to pause/resume the active session even when browsing other devices.
		currentState := s.getScreenState()
		isActivePlayback := currentState == "Playing" || currentState == "Paused"
		if s.selectedDevice.addr != "" && s.selectedDevice.addr != data[id].addr && !isActivePlayback {
			// Clear DLNA-specific state only
			s.controlURL = ""
			s.eventlURL = ""
			s.renderingControlURL = ""
			s.connectionManagerURL = ""
			s.tvdata = nil
		}

		s.selectedDevice = data[id]
		s.selectedDeviceType = data[id].deviceType

		if data[id].deviceType == devices.DeviceTypeDLNA {
			t, err := soapcalls.DMRextractor(context.Background(), data[id].addr)
			check(s, err)
			if err == nil {
				s.controlURL = t.AvtransportControlURL
				s.eventlURL = t.AvtransportEventSubURL
				s.renderingControlURL = t.RenderingControlURL
				s.connectionManagerURL = t.ConnectionManagerURL
				if s.tvdata != nil {
					s.tvdata.RenderingControlURL = s.renderingControlURL
				}
			}
		}

		// Auto-enable transcoding for incompatible Chromecast media
		if data[id].deviceType == devices.DeviceTypeChromecast && s.mediafile != "" {
			s.checkChromecastCompatibility()
		}
	}

	transcode.OnChanged = func(b bool) {
		s.Transcode = b
	}

	sfilecheck.OnChanged = func(b bool) {
		if b {
			sbrowse.Enable()
			return
		}

		sbrowse.Disable()
	}

	var mediafileOld, mediafileOldText string

	externalmedia.OnChanged = func(b bool) {
		if b {
			nextmedia.SetChecked(false)
			nextmedia.Disable()
			mbrowse.Disable()
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

		mbrowse.Enable()
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
	refreshDevices := time.NewTicker(2 * time.Second)

	_, err := getDevices(1)
	if err != nil && !errors.Is(err, devices.ErrNoDeviceAvailable) {
		check(s, err)
	}

	for range refreshDevices.C {
		newDevices, _ := getDevices(1)

		var oldDevices []devType
		var selectedAddr string
		var selectedDeviceAddr string
		fyne.DoAndWait(func() {
			oldDevices = append([]devType(nil), (*data)...)
			selectedDeviceAddr = s.selectedDevice.addr
			selectedAddr = s.controlURL
			if s.selectedDeviceType == devices.DeviceTypeChromecast {
				selectedAddr = selectedDeviceAddr
			}
		})

	outer:
		for _, old := range oldDevices {
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
		}

		// check to see if the new refresh includes one of the already selected devices
		var includes bool
		if selectedAddr != "" {
			u, _ := url.Parse(selectedAddr)
			for _, d := range newDevices {
				n, _ := url.Parse(d.addr)
				if n.Host == u.Host {
					includes = true
				}
			}
		}

		clearSelection := false
		if selectedAddr != "" && !includes {
			u, _ := url.Parse(selectedAddr)
			if !utils.HostPortIsAlive(u.Host) {
				clearSelection = true
			}
		}

		foundIdx := -1
		if selectedDeviceAddr != "" {
			for n, a := range newDevices {
				if selectedDeviceAddr == a.addr {
					foundIdx = n
					break
				}
			}
		}

		fyne.DoAndWait(func() {
			*data = newDevices

			if clearSelection {
				s.controlURL = ""
				s.selectedDevice = devType{}
				s.DeviceList.UnselectAll()
			} else if foundIdx >= 0 {
				s.DeviceList.Select(foundIdx)
			} else {
				s.DeviceList.UnselectAll()
			}

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
			// Don't reset slider for Chromecast - it has its own status watcher
			if s.selectedDeviceType == devices.DeviceTypeChromecast && s.chromecastClient != nil {
				continue
			}
			fyne.Do(func() {
				s.SlideBar.Slider.SetValue(0)
				s.CurrentPos.Set("00:00:00")
				s.EndPos.Set("00:00:00")
			})
		}

		if s.State == "Playing" {
			// Skip for Chromecast - it has its own status watcher (chromecastStatusWatcher)
			if s.selectedDeviceType == devices.DeviceTypeChromecast {
				continue
			}

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
