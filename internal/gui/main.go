//go:build !(android || ios)

package gui

import (
	"context"
	"errors"
	"math"
	"net/url"
	"sort"
	"sync"
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

type tappedSlider struct {
	*widget.Slider
	screen *FyneScreen
	end    string
	ccDur  float64
	mu     sync.Mutex
}

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
		} else {
			row.setLeadingIcon(castIcon())
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

func (t *tappedSlider) chromecastSeekDuration() (float64, bool) {
	if t.screen.mediaDuration > 0 {
		return t.screen.mediaDuration, true
	}

	t.mu.Lock()
	cached := t.ccDur
	t.mu.Unlock()
	if cached > 0 {
		return cached, true
	}

	client := t.screen.activeChromecastPlaybackClient()
	if client == nil {
		return 0, false
	}

	status, err := client.GetStatus()
	if err != nil || status.Duration <= 0 {
		return 0, false
	}

	duration := float64(status.Duration)

	t.mu.Lock()
	t.ccDur = duration
	t.mu.Unlock()

	// Cache briefly while dragging to avoid hammering GetStatus.
	go func() {
		time.Sleep(time.Second)
		t.mu.Lock()
		t.ccDur = 0
		t.mu.Unlock()
	}()

	return duration, true
}

func (t *tappedSlider) Dragged(e *fyne.DragEvent) {
	t.Slider.Dragged(e)
	t.screen.sliderActive = true

	if duration, ok := t.chromecastSeekDuration(); ok {
		cur := (duration * t.Slider.Value) / t.Slider.Max
		reltime := utils.SecondsToClockTime(int(cur))
		total := utils.SecondsToClockTime(int(duration))
		t.screen.CurrentPos.Set(reltime)
		t.screen.EndPos.Set(total)
		return
	}

	// DLNA: Get position from device
	t.mu.Lock()
	cachedEnd := t.end
	t.mu.Unlock()

	if cachedEnd == "" {
		if t.screen.tvdata == nil {
			return
		}
		getSliderPos, err := t.screen.tvdata.GetPositionInfo()
		if err != nil {
			return
		}

		t.mu.Lock()
		t.end = getSliderPos[0]
		cachedEnd = t.end
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

	total, err := utils.ClockTimeToSeconds(cachedEnd)
	if err != nil {
		return
	}

	cur := (float64(total) * t.Slider.Value) / t.Slider.Max
	roundedInt := int(math.Round(cur))

	reltime := utils.SecondsToClockTime(roundedInt)

	end, err := utils.FormatClockTime(cachedEnd)
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
		releasePermit, permitted := t.screen.rendererPermit(false)
		if !permitted {
			return
		}
		defer releasePermit()

		// Handle Chromecast seeking
		if client := t.screen.activeChromecastPlaybackClient(); client != nil {
			duration, ok := t.chromecastSeekDuration()
			if !ok {
				return
			}
			seekPos := int((t.screen.SlideBar.Value / t.screen.SlideBar.Max) * duration)
			// Transcoded seek: use optimized helper that keeps connection open
			// (Chromecast's native Seek() doesn't work on transcoded streams)
			if t.screen.mediaDuration > 0 {
				chromecastTranscodedSeek(t.screen, seekPos)
				return
			}
			// Non-transcoded seek: use Chromecast's native seek
			if err := client.Seek(seekPos); err != nil {
				return
			}
			return
		}

		t.seekDLNAAsync()
	}
}

func (t *tappedSlider) Tapped(p *fyne.PointEvent) {
	// The auto-refresh action should reset this back to false
	// after the first iterration.
	t.screen.sliderActive = true

	t.Slider.Tapped(p)

	if t.screen.State == "Playing" || t.screen.State == "Paused" {
		releasePermit, permitted := t.screen.rendererPermit(false)
		if !permitted {
			return
		}
		defer releasePermit()

		// Handle Chromecast seeking
		if client := t.screen.activeChromecastPlaybackClient(); client != nil {
			duration, ok := t.chromecastSeekDuration()
			if !ok {
				return
			}

			seekPos := int((t.screen.SlideBar.Value / t.screen.SlideBar.Max) * duration)

			// Update time labels immediately for visual feedback (like DLNA)
			current := utils.SecondsToClockTime(seekPos)
			total := utils.SecondsToClockTime(int(duration))
			fyne.Do(func() {
				t.screen.CurrentPos.Set(current)
				t.screen.EndPos.Set(total)
			})

			// Transcoded seek: use optimized helper that keeps connection open
			if t.screen.mediaDuration > 0 {
				chromecastTranscodedSeek(t.screen, seekPos)
				return
			}

			// Non-transcoded seek: use Chromecast's native seek
			if err := client.Seek(seekPos); err != nil {
				return
			}

			return
		}

		t.seekDLNAAsync()
	}
}

func (t *tappedSlider) seekDLNAAsync() {
	if t.screen.tvdata == nil {
		return
	}

	tvdata := t.screen.tvdata
	sliderValue := t.screen.SlideBar.Value
	sliderMax := t.screen.SlideBar.Max
	if sliderMax == 0 {
		return
	}
	isTranscode := tvdata.Transcode

	releasePermit, permitted := t.screen.rendererPermit(false)
	if !permitted {
		return
	}

	go func() {
		defer releasePermit()
		getPos, err := tvdata.GetPositionInfo()
		if err != nil {
			return
		}

		total, err := utils.ClockTimeToSeconds(getPos[0])
		if err != nil {
			return
		}

		cur := (float64(total) * sliderValue) / sliderMax
		roundedInt := int(math.Round(cur))

		reltime := utils.SecondsToClockTime(roundedInt)

		end, err := utils.FormatClockTime(getPos[0])
		if err != nil {
			return
		}

		fyne.Do(func() {
			t.screen.CurrentPos.Set(reltime)
			t.screen.EndPos.Set(end)
		})

		if isTranscode {
			// playAction reads these from its own goroutine, so set them
			// here instead of inside the queued fyne.Do above.
			t.screen.ffmpegSeek = roundedInt
			t.screen.dlnaSeekRestart = true

			// Live-transcoded streams are advertised as non-seekable
			// (DLNA.ORG_OP=00), so renderers reject Seek with error 701.
			// Restart the session at the new offset instead, waiting for
			// the old session's teardown so its Stop cannot race with the
			// new SetAVTransportURI/Play.
			stopActionSync(t.screen)
			playAction(t.screen)
			return
		}

		_ = tvdata.SeekSoapCall(reltime)
	}()
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

	playpause := widget.NewButtonWithIcon(lang.L("Cast")+"   ", theme.MediaPlayIcon(), func() {
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

	clearmedia := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		clearmediaAction(s)
	})

	clearsubs := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		clearsubsAction(s)
	})

	skipPrevious := widget.NewButtonWithIcon(lang.L("Previous"), theme.MediaSkipPreviousIcon(), func() {
		skipPreviousAction(s)
	})
	skipPrevious.Importance = widget.LowImportance
	skipPrevious.Alignment = widget.ButtonAlignCenter

	skipNext := widget.NewButtonWithIcon(lang.L("Next"), theme.MediaSkipNextIcon(), func() {
		skipNextAction(s)
	})
	skipNext.Importance = widget.LowImportance
	skipNext.Alignment = widget.ButtonAlignCenter

	queueButton := widget.NewButton(lang.L("Playlist"), func() {
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
	transcode := ttwidget.NewCheck(lang.L("Transcode"), func(b bool) {})
	screencast := ttwidget.NewCheck(lang.L("Cast Desktop (experimental)"), func(b bool) {})
	rtmpServerCheck := ttwidget.NewCheck(lang.L("Enable RTMP Server"), func(b bool) {
		if b {
			startRTMPServer(s)
		} else {
			stopRTMPServer(s)
		}
	})
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

	mediafilelabel := widget.NewLabel(lang.L("Media File") + ":")
	subsfilelabel := widget.NewLabel(lang.L("Subtitles") + ":")

	selectInternalSubs := widget.NewSelect([]string{}, func(item string) {
		if item == "" {
			return
		}
		s.SubsText.SetText("")
		s.subsfile = ""
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

	actionbuttons := container.NewHBox(
		skipPrevious,
		playpause,
		stop,
		skipNext,
		queueButton,
		layout.NewSpacer(),
		volumedown,
		volumeup,
		muteunmute,
	)

	mrightwidgets := container.NewHBox(previewmedia, clearmedia, mbrowse)
	srightwidgets := container.NewHBox(selectInternalSubs, clearsubs, sbrowse)

	mfiletextArea := container.New(layout.NewBorderLayout(nil, nil, nil, mrightwidgets), mrightwidgets, mfiletext)
	sfiletextArea := container.New(layout.NewBorderLayout(nil, nil, nil, srightwidgets), srightwidgets, sfiletext)
	viewfilescont := container.New(layout.NewFormLayout(), mediafilelabel, mfiletextArea, subsfilelabel, sfiletextArea)

	mediaCard := widget.NewCard(lang.L("Media"), "", viewfilescont)

	commonCard := widget.NewCard(lang.L("Common Options"), "", container.NewVBox(medialoop, nextmedia))

	advancedCard := widget.NewCard(lang.L("Advanced Options"), "", container.NewGridWithColumns(2, container.NewVBox(externalmedia, sfilecheck, transcode), container.NewVBox(screencast, rtmpServerCheck)))

	playCard := widget.NewCard(lang.L("Playback"), "", container.NewVBox(sliderArea, actionbuttons))

	deviceHeader := widget.NewLabelWithStyle(lang.L("(auto refreshing)"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	s.ActiveDeviceLabel = widget.NewLabel("")
	s.ActiveDeviceLabel.Wrapping = fyne.TextWrapWord
	s.ActiveDeviceIcon = widget.NewIcon(theme.MediaPlayIcon())
	s.ActiveDeviceStopSession = widget.NewButtonWithIcon(lang.L("Stop Session"), theme.MediaStopIcon(), func() {
		s.ActiveDeviceStopSession.Disable()
		s.stopRemoteWebSession()
	})
	s.ActiveDeviceStopSession.Importance = widget.DangerImportance
	s.ActiveDeviceStopSession.Hide()
	s.ActiveDeviceCard = widget.NewCard(lang.L("Active Device"), "",
		container.NewBorder(nil, nil, s.ActiveDeviceIcon, container.NewCenter(s.ActiveDeviceStopSession), s.ActiveDeviceLabel))
	s.ActiveDeviceCard.Hide()

	deviceBottom := container.NewVBox(s.ActiveDeviceCard, s.rtmpURLCard)
	deviceCard := widget.NewCard(lang.L("Devices"), "", container.NewBorder(deviceHeader, deviceBottom, nil, nil, list))

	topCards := container.NewVBox(mediaCard, playCard, commonCard)
	leftColumn := container.NewBorder(topCards, nil, nil, nil, advancedCard)
	content := container.New(&RatioLayout{LeftRatio: 0.66}, leftColumn, deviceCard)

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

	var mediafileOld, mediafileOldText string

	externalmedia.OnChanged = func(b bool) {
		if b {
			nextmedia.SetChecked(false)
			nextmedia.Disable()
			mbrowse.Disable()
			previewmedia.Disable()
			skipNext.Disable()
			skipPrevious.Disable()

			// keep old values
			mediafileOld = s.mediafile
			mediafileOldText = s.MediaText.Text

			// rename the label
			mediafilelabel.Text = lang.L("URL") + ":"
			mediafilelabel.Refresh()

			// Clear the Media Text Area
			clearCurrentMediaSelection(s)

			// Set some Media text defaults
			// to indicate that we're expecting a URL
			s.MediaText.SetPlaceHolder(lang.L("Enter URL here"))
			s.MediaText.Enable()
			setPlayPauseView("", s)
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
		mediafilelabel.Text = lang.L("Media File") + ":"
		s.MediaText.SetPlaceHolder("")
		mediafilelabel.Refresh()
		s.MediaText.Disable()
		restoreMediaInputState(s, mediafileOld, mediafileOldText)
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
	return content
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

func sliderUpdate(s *FyneScreen) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		if s.sliderActive {
			s.sliderActive = false
			continue
		}

		if (s.State == "Stopped" || s.State == "") && s.ffmpegSeek == 0 {
			// Don't reset slider for Chromecast - it has its own status watcher
			if s.chromecastSessionClient() != nil {
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
			if s.activeChromecastPlaybackClient() != nil {
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

				currentClock := utils.SecondsToClockTime(current)

				fyne.Do(func() {
					s.CurrentPos.Set(currentClock)
					s.EndPos.Set(end)
				})
				s.persistResumeProgress(current, float64(total), false)
			}
		}
	}
}
