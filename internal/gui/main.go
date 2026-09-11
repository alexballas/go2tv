//go:build !(android || ios)

package gui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"time"

	ttwidget "github.com/alexballas/fyne-tooltip/widget"
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/layout"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
	"golang.org/x/time/rate"
)

type deviceList struct {
	widget.List
}

func (c *deviceList) FocusGained() {}

// sortDevTypeSlice sorts devices alphabetically by name,
// with DLNA devices before Chromecast devices when names are equal.
func sortDevTypeSlice(d []devType) {
	sort.Slice(d, func(i, j int) bool {
		if d[i].deviceType != d[j].deviceType {
			return d[i].deviceType < d[j].deviceType
		}
		return d[i].name < d[j].name
	})
}

func newDeviceList(s *FyneScreen, dd *[]devType) *deviceList {
	list := &deviceList{}

	list.Length = func() int {
		return len(*dd)
	}

	list.CreateItem = func() fyne.CanvasObject {
		return newDeviceRow(castIcon(), nil)
	}

	list.UpdateItem = func(i widget.ListItemID, o fyne.CanvasObject) {
		row := o.(*deviceRow)

		item := (*dd)[i]
		row.setDevice(item)

		// Determine if this device owns the active session.
		isActive := false
		currentState := s.getScreenState()
		isActivePlayback := currentState == "Playing" || currentState == "Paused"
		activeDevice := s.getActiveDevice()
		if isActivePlayback && activeDevice.addr != "" {
			isActive = item.addr == activeDevice.addr && item.deviceType == activeDevice.deviceType
		}

		// Swap icon based on state
		if isActive {
			row.setLeadingIcon(theme.MediaPlayIcon())
		} else if item.addr == s.selectedDevice.addr {
			row.setLeadingIcon(theme.ConfirmIcon())
		} else {
			row.setLeadingIcon(castIcon())
		}
	}

	list.ExtendBaseWidget(list)
	return list
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
		if !s.Hotkeys || s.hotkeysSuspended() {
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

		if k.Name == "B" && s.SkipPreviousButton != nil {
			s.SkipPreviousButton.Tapped(fynePE)
		}
	})

	// Avoid parallel execution of getDevices.
	blockGetDevices := make(chan struct{})
	go func() {
		datanew, err := getDevices()
		if err != nil {
			datanew = nil
		}

		// Sort devices alphabetically for consistent ordering
		sortDevTypeSlice(datanew)

		fyne.DoAndWait(func() {
			data = datanew
			list.Refresh()
		})

		blockGetDevices <- struct{}{}
	}()

	mfiletext := widget.NewEntry()
	mfiletext.OnChanged = func(v string) {
		if s.ExternalMediaURL != nil && s.ExternalMediaURL.Checked &&
			s.TranscodeCheckBox != nil && s.TranscodeCheckBox.Checked &&
			utils.IsHLSStream(v, "") {
			s.TranscodeCheckBox.SetChecked(false)
		}
		setPlayPauseView("", s)
	}
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

	playpause := ttwidget.NewButtonWithIcon(lang.L("Cast")+"   ", theme.MediaPlayIcon(), func() {
		playAction(s)
	})
	playpause.Importance = widget.HighImportance
	// playpause.Alignment = widget.ButtonAlignCenter

	stop := widget.NewButtonWithIcon(lang.L("Stop"), theme.MediaStopIcon(), func() {
		stopAction(s)
	})
	stop.Importance = widget.LowImportance
	stop.Alignment = widget.ButtonAlignCenter

	volumeup := widget.NewButtonWithIcon("", theme.VolumeUpIcon(), func() {
		volumeAction(s, true)
	})
	volumeup.Importance = widget.LowImportance
	volumeup.Alignment = widget.ButtonAlignCenter

	muteunmute := widget.NewButtonWithIcon("", theme.VolumeMuteIcon(), func() {
		muteAction(s)
	})
	muteunmute.Importance = widget.LowImportance
	muteunmute.Alignment = widget.ButtonAlignCenter

	volumedown := widget.NewButtonWithIcon("", theme.VolumeDownIcon(), func() {
		volumeAction(s, false)
	})
	volumedown.Importance = widget.LowImportance
	volumedown.Alignment = widget.ButtonAlignCenter

	clearmedia := widget.NewButton(lang.L("Clear"), func() {
		clearmediaAction(s)
	})

	clearsubs := widget.NewButton(lang.L("Clear"), func() {
		clearsubsAction(s)
	})

	skipPrevious := widget.NewButtonWithIcon("", theme.MediaSkipPreviousIcon(), func() {
		skipPreviousAction(s)
	})
	skipPrevious.Importance = widget.LowImportance
	skipPrevious.Alignment = widget.ButtonAlignCenter

	skipNext := widget.NewButtonWithIcon("", theme.MediaSkipNextIcon(), func() {
		skipNextAction(s)
	})
	skipNext.Importance = widget.LowImportance
	skipNext.Alignment = widget.ButtonAlignCenter

	queueButton := widget.NewButtonWithIcon(lang.L("Playlist"), theme.ListIcon(), func() {
		s.openQueueWindow()
	})
	queueButton.Importance = widget.MediumImportance
	sliderBar := newTappableSlider(s)

	// previewmedia spawns external applications.
	// Since there is no way to monitor the time it takes
	// for the apps to load, we introduce a rate limit
	// for the specific action.
	throttle := rate.Every(3 * time.Second)
	r := rate.NewLimiter(throttle, 1)
	previewmedia := widget.NewButton(lang.L("Preview"), func() {
		if !r.Allow() {
			return
		}
		go previewmedia(s)
	})

	sfilecheck := widget.NewCheck(lang.L("Manual Subtitles"), func(b bool) {})
	externalmedia := widget.NewCheck(lang.L("Media from URL"), func(b bool) {})
	loopToggle := newPlaybackToggle(lang.L("Loop"), playbackLoopIcon())
	nextToggle := newPlaybackToggle(lang.L("Auto-play"), playbackAutoplayIcon())
	medialoop, nextmedia := &loopToggle.Check.Check, &nextToggle.Check.Check
	transcodeToggle := newPlaybackToggle(lang.L("Transcode"), playbackTranscodeIcon())
	transcode := &transcodeToggle.Check
	screencastToggle := newPlaybackToggle(lang.L("Cast Desktop (experimental)"), playbackDesktopIcon())
	screencast := &screencastToggle.Check
	rtmpToggle := newPlaybackToggle(lang.L("RTMP Server"), playbackServerIcon())
	rtmpServerCheck := &rtmpToggle.Check
	rtmpServerCheck.OnChanged = func(b bool) {
		if b {
			startRTMPServer(s)
		} else {
			stopRTMPServer(s)
		}
	}
	s.rtmpServerCheck = &rtmpServerCheck.Check
	s.transcodeToolTipCheck = transcode
	s.screencastToolTipCheck = screencast
	s.rtmpServerToolTipCheck = rtmpServerCheck
	if err := s.ffmpegStatus(); err != nil {
		s.rtmpServerCheck.Disable()
		screencast.Disable()
	}
	s.updateFFmpegDependentCheckTooltips()

	s.rtmpURLEntry = widget.NewEntry()
	s.rtmpURLEntry.Disable()
	copyURLBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
		fyne.CurrentApp().Clipboard().SetContent(s.rtmpURLEntry.Text)
	})

	s.rtmpKeyEntry = widget.NewEntry()
	s.rtmpKeyEntry.Password = true
	s.rtmpKeyEntry.Disable()
	copyKeyBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
		fyne.CurrentApp().Clipboard().SetContent(s.rtmpKeyEntry.Text)
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

	selectInternalSubs := widget.NewSelect([]string{}, nil)

	selectInternalSubs.PlaceHolder = lang.L("No Embedded Subs")
	selectInternalSubs.Disable()

	curPos := binding.NewString()
	endPos := binding.NewString()

	s.PlayPause = &playpause.Button
	s.playPauseToolTip = playpause
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
	s.SkipPreviousButton = skipPrevious
	s.SkipNextButton = skipNext
	s.SlideBar = sliderBar
	s.CurrentPos = curPos
	s.EndPos = endPos
	s.SelectInternalSubs = selectInternalSubs
	s.TranscodeCheckBox = &transcode.Check
	s.ScreencastCheckBox = &screencast.Check
	s.LoopSelectedCheck = medialoop
	s.MediaBrowse = mbrowse
	s.QueueButton = queueButton
	s.ClearMedia = clearmedia
	s.SubsBrowse = sbrowse

	curPos.Set("00:00:00")
	endPos.Set("00:00:00")

	setPlayPauseView("", s)
	s.refreshQueueStateUI()

	sliderArea := container.NewBorder(nil, nil, widget.NewLabelWithData(curPos), widget.NewLabelWithData(endPos), sliderBar)

	volumedown.SetText("−")
	volumeup.SetText("+")
	volumedown.SetIcon(nil)
	volumeup.SetIcon(nil)
	// Reserve both labels so toggling mute never shifts the volume controls.
	muteunmute.SetText(lang.L("Unmute"))
	muteSize := muteunmute.MinSize()
	muteunmute.SetText(lang.L("Mute"))
	muteSize = muteSize.Max(muteunmute.MinSize())
	muteControl := container.NewGridWrap(muteSize, muteunmute)
	volumeRow := container.NewHBox(widget.NewLabel(lang.L("Volume")), volumedown, volumeup, muteControl)
	transportRow := container.NewHBox(playpause, stop, skipPrevious, skipNext)
	actionButtons := container.New(layout.NewCustomPaddedLayout(0, 0, theme.InnerPadding(), 0), container.NewBorder(nil, nil, transportRow, volumeRow))
	s.playbackStatus = newPlaybackStatusLabel(lang.L("Select a device"))
	s.playbackStatus.Importance = widget.MediumImportance
	s.playbackStatus.Truncation = fyne.TextTruncateEllipsis

	mediaSelection := newMediaSelectionCard(s, previewmedia, clearsubs)
	mediaCard := newSectionCard(lang.L("Media"), mediaSelection.content)

	commonOptions := container.New(playbackModesLayout{}, loopToggle, nextToggle)
	commonHeader := widget.NewRichText(&widget.TextSegment{Text: lang.L("Common Options"), Style: widget.RichTextStyle{SizeName: theme.SizeNameCaptionText}})

	advancedOptions := container.NewVBox(container.New(playbackModesLayout{}, transcodeToggle, screencastToggle, rtmpToggle))
	advancedHeader := widget.NewRichText(&widget.TextSegment{Text: lang.L("Advanced Options"), Style: widget.RichTextStyle{SizeName: theme.SizeNameCaptionText}})

	s.selectedArtwork = newSelectedArtwork()
	playbackControls := container.New(
		layout.NewCustomPaddedLayout(0, 0, 0, 0), container.NewVBox(s.playbackStatus, sliderArea, actionButtons),
	)
	playbackRow := container.New(artworkPlaybackLayout{}, s.selectedArtwork, playbackControls)
	playCard := newSectionCard(lang.L("Playback"), container.New(
		layout.NewCustomPaddedLayout(8, 8, 0, 8),
		container.New(layout.NewCustomPaddedVBoxLayout(12),
			playbackRow,
			widget.NewSeparator(),
			container.NewVBox(commonHeader, commonOptions),
			widget.NewSeparator(),
			container.NewVBox(advancedHeader, advancedOptions),
		),
	))
	playCard.headerAction = queueButton

	deviceHeader := widget.NewLabel(lang.L("Searching for devices…"))
	s.deviceSummary = deviceHeader
	deviceHeader.Importance = widget.MediumImportance

	s.ActiveDeviceLabel = widget.NewLabel("")
	s.ActiveDeviceLabel.Wrapping = fyne.TextWrapWord
	s.ActiveDeviceIcon = widget.NewIcon(theme.MediaPlayIcon())
	s.ActiveDeviceCard = widget.NewCard(lang.L("Active Device"), "",
		container.NewBorder(nil, nil, s.ActiveDeviceIcon, nil, s.ActiveDeviceLabel))
	s.ActiveDeviceCard.Hide()

	deviceBottom := container.NewVBox(s.ActiveDeviceCard, s.rtmpURLCard)
	deviceCard := newSectionCard(lang.L("Devices"), container.NewBorder(deviceHeader, deviceBottom, nil, nil, list))

	leftColumn := container.NewBorder(mediaCard, nil, nil, nil, playCard)
	mainLayout := newResponsiveTwoColumnLayout(800, 0.72)
	mainLayout.narrowTrailingMinHeight = 240
	content := container.New(mainLayout, leftColumn, deviceCard)

	// Widgets actions
	list.OnSelected = func(id widget.ListItemID) {
		// Only reset DLNA-specific state when switching devices, NOT Chromecast playback.
		// This allows browsing the device list while Chromecast is playing.
		// Once playback is stopped, switching away should drop the warm Chromecast session.
		// Also don't reset tvdata if something is currently playing - user should be able
		// to pause/resume the active session even when browsing other devices.
		currentState := s.getScreenState()
		isActivePlayback := currentState == "Playing" || currentState == "Paused"
		if s.selectedDevice.addr != "" && s.selectedDevice.addr != data[id].addr && !isActivePlayback {
			// Clear DLNA-specific state only
			s.controlURL = ""
			s.eventURL = ""
			s.renderingControlURL = ""
			s.connectionManagerURL = ""
			s.tvdata = nil

			if s.chromecastClient != nil && s.chromecastClient.IsConnected() {
				client := s.chromecastClient
				server := s.httpserver
				s.chromecastClient = nil
				s.httpserver = nil
				go func() {
					_ = client.Close(false)
					if server != nil {
						server.StopServer()
					}
				}()
			}
		}

		s.selectedDevice = data[id]
		s.selectedDeviceType = data[id].deviceType

		if data[id].deviceType == devices.DeviceTypeDLNA {
			t, err := soapcalls.DMRextractor(context.Background(), data[id].addr)
			check(s, err)
			if err == nil {
				s.controlURL = t.AvtransportControlURL
				s.eventURL = t.AvtransportEventSubURL
				s.renderingControlURL = t.RenderingControlURL
				s.connectionManagerURL = t.ConnectionManagerURL
				if s.tvdata != nil && !isActivePlayback {
					s.tvdata.RenderingControlURL = s.renderingControlURL
				}
			}
		}

		// Auto-enable transcoding for incompatible Chromecast media
		if data[id].deviceType == devices.DeviceTypeChromecast && s.mediafile != "" {
			s.checkChromecastCompatibility()
		}
		setPlayPauseView("", s)
		list.Refresh()
	}

	transcode.OnChanged = func(b bool) {
		s.Transcode = b
	}

	screencast.OnChanged = func(b bool) {
		if b {
			if err := s.validateFFmpeg(); err != nil {
				check(s, errors.New(lang.L("ffmpeg is required for screencast")))
				screencast.SetChecked(false)
				return
			}

			s.screencastPrevTranscode = transcode.Checked
			s.screencastPrevExternal = externalmedia.Checked
			s.screencastPrevManualSubs = sfilecheck.Checked
			s.screencastPrevLoop = medialoop.Checked
			s.screencastPrevNext = nextmedia.Checked
			s.screencastPrevMediaText = s.MediaText.Text
			s.screencastPrevMediaFile = s.mediafile

			s.Screencast = true
			s.Transcode = true
			transcode.SetChecked(true)
			transcode.Disable()
			medialoop.SetChecked(false)
			nextmedia.SetChecked(false)
			medialoop.Disable()
			nextmedia.Disable()
			if s.rtmpServerCheck != nil {
				s.rtmpServerCheck.SetChecked(false)
				s.rtmpServerCheck.Disable()
			}
			s.SlideBar.Disable()
			externalmedia.SetChecked(true)
			externalmedia.Disable()
			mbrowse.Disable()
			clearmedia.Disable()
			s.MediaText.Disable()
			s.MediaText.SetPlaceHolder("")
			s.selectArtwork("")
			s.MediaText.SetText(lang.L("Cast Desktop Live Stream"))
			s.mediafile = lang.L("Cast Desktop Live Stream")
			sfilecheck.SetChecked(false)
			s.subsfile = ""
			s.SubsText.SetText("")
			setPlayPauseView("", s)
			return
		}

		s.Screencast = false
		go stopScreencastSession(s)
		if err := s.ffmpegStatus(); err == nil && s.rtmpServer == nil {
			transcode.Enable()
			externalmedia.Enable()
			sfilecheck.Enable()
			transcode.SetChecked(s.screencastPrevTranscode)
			externalmedia.SetChecked(s.screencastPrevExternal)
			sfilecheck.SetChecked(s.screencastPrevManualSubs)
			medialoop.SetChecked(s.screencastPrevLoop)
			nextmedia.SetChecked(s.screencastPrevNext)

			if s.ExternalMediaURL != nil && !s.ExternalMediaURL.Checked {
				if !nextmedia.Checked {
					medialoop.Enable()
				}
				if !medialoop.Checked {
					nextmedia.Enable()
				}
			}

			if s.ExternalMediaURL != nil && s.ExternalMediaURL.Checked {
				mbrowse.Disable()
				s.MediaText.Enable()
			} else {
				mbrowse.Enable()
				s.MediaText.Disable()
			}
			clearmedia.Enable()
			s.SlideBar.Enable()
			s.MediaText.SetPlaceHolder("")
			if s.screencastPrevExternal {
				restoreMediaInputState(s, s.screencastPrevMediaFile, s.screencastPrevMediaText)
			}
			if s.rtmpServerCheck != nil {
				s.rtmpServerCheck.Enable()
			}
		}
	}

	sfilecheck.OnChanged = func(b bool) {
		if b {
			sbrowse.Enable()
			return
		}

		sbrowse.Disable()
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
			s.refreshImageAutoSkipTimer()
		case false:
			medialoop.Enable()
			s.cancelImageAutoSkipTimer()
		}

		go func() {
			gaplessOption := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")
			target := traversalPlaybackTarget(s)

			if b {
				if gaplessOption == "Enabled" && target.device.deviceType == devices.DeviceTypeDLNA {
					switch s.getScreenState() {
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

			if target.device.deviceType == devices.DeviceTypeDLNA && s.tvdata != nil && s.tvdata.CallbackURL != "" {
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

	status := newRemoteSessionStatusView(s, content)
	s.remoteSessionStatus = status
	s.bindRemoteSessionStatus()
	return status.root
}

func refreshDevList(s *FyneScreen, data *[]devType) {
	refreshDevices := time.NewTimer(0)

	_, err := getDevices()
	if err != nil && !errors.Is(err, devices.ErrNoDeviceAvailable) {
		check(s, err)
	}

	for range refreshDevices.C {
		newDevices, _ := getDevices()

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

		// Sort devices alphabetically for consistent ordering
		sortDevTypeSlice(newDevices)

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
			if s.deviceSummary != nil {
				s.deviceSummary.SetText(fmt.Sprintf(lang.L("%d devices found"), len(newDevices)))
			}
			if clearSelection {
				setPlayPauseView("", s)
			}
		})

		refreshDevices.Reset(time.Second)
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
			setMuteUnmuteView(true, s)
		case "0":
			setMuteUnmuteView(false, s)
		}
	}
}
