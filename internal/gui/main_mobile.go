//go:build android || ios

package gui

import (
	"context"
	"errors"
	"image/color"
	"net/url"
	"sort"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/layout"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
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

		state := s.getScreenState()
		active := s.getActiveDevice()
		switch {
		case (state == "Playing" || state == "Paused") && item.addr == active.addr && item.deviceType == active.deviceType:
			row.setLeadingIcon(playingIcon())
		case item.addr == s.selectedDevice.addr:
			row.setLeadingIcon(theme.ConfirmIcon())
		default:
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
	discoveryStatus := widget.NewLabel(discoveryStatusText(0, false, nil))
	discoveryStatus.Alignment = fyne.TextAlignTrailing
	noDevicesHint := widget.NewLabel(lang.L("Make sure your phone and the TV are on the same Wi-Fi network."))
	noDevicesHint.Alignment = fyne.TextAlignCenter
	noDevicesHint.Wrapping = fyne.TextWrapWord
	noDevicesHint.Hide()
	rescan := make(chan struct{}, 1)
	var refreshDevices *widget.Button
	refreshDevices = widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		refreshDevices.Disable()
		discoveryStatus.SetText(lang.L("Searching for devices…"))
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = devices.RefreshDiscovery(ctx)
			cancel()
			select {
			case rescan <- struct{}{}:
			default:
			}
			fyne.Do(refreshDevices.Enable)
		}()
	})
	refreshDevices.Importance = widget.LowImportance

	// Avoid parallel execution of getDevices.
	blockGetDevices := make(chan struct{})
	go func() {
		datanew, err := getDevices()
		searched := err == nil
		if errors.Is(err, devices.ErrNoDeviceAvailable) {
			datanew = nil
			err = nil
			searched = true
		}

		// Sort devices alphabetically for consistent ordering
		sortDevTypeSlice(datanew)

		fyne.DoAndWait(func() {
			data = datanew
			list.Refresh()
			discoveryStatus.SetText(discoveryStatusText(len(datanew), searched, err))
			noDevicesHint.Hidden = !searched || len(datanew) > 0
			noDevicesHint.Refresh()
		})

		blockGetDevices <- struct{}{}
	}()

	// The entries hold the selection; read-only modes show valueFields.
	mfiletext := widget.NewEntry()
	mfiletext.SetPlaceHolder(lang.L("Enter URL here"))
	sfiletext := widget.NewEntry()
	mediaField := newValueField(nil)
	subsField := newValueField(func() { subsAction(s) })
	subsField.Set("", lang.L("No subtitle selected"))
	sfiletext.OnChanged = func(text string) { subsField.Set(text, lang.L("No subtitle selected")) }

	mfile := widget.NewButton(lang.L("Select Media File"), func() {
		mediaAction(s)
	})

	sfile := widget.NewButton(lang.L("Select Subtitles File"), func() {
		subsAction(s)
	})

	playpause := widget.NewButtonWithIcon(lang.L("Play"), theme.MediaPlayIcon(), func() {
		hasMedia := s.mediafile != nil || (s.MediaText != nil && s.MediaText.Text != "")
		switch playbackPreflight(
			s.getScreenState(),
			hasMedia,
			s.selectedDeviceType,
			s.selectedDevice.addr,
			s.controlURL,
		) {
		case playbackPreflightMissingMedia:
			check(w, errors.New(lang.L("please select a media file or enter a media URL")))
			return
		case playbackPreflightMissingDevice:
			check(w, errors.New(lang.L("please select a device")))
			return
		}

		startMobilePlayback(s)
	})

	playpause.Importance = widget.HighImportance

	stop := widget.NewButtonWithIcon("", theme.MediaStopIcon(), func() {
		stopAction(s)
	})

	volumeup := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		volumeAction(s, true)
	})

	muteunmute := widget.NewButtonWithIcon("", theme.VolumeUpIcon(), func() {
		muteAction(s)
	})

	volumedown := widget.NewButtonWithIcon("", theme.ContentRemoveIcon(), func() {
		volumeAction(s, false)
	})

	clearmedia := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		clearmediaAction(s)
	})

	clearsubs := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		clearsubsAction(s)
	})

	sliderBar := newTappableSlider(s)
	curPos := binding.NewString()
	endPos := binding.NewString()

	externalmedia := widget.NewCheck(lang.L("Media from URL"), func(b bool) {})
	medialoop := widget.NewCheck(lang.L("Loop"), func(b bool) {})
	transcode := widget.NewCheck(lang.L("Transcode"), func(b bool) {
		s.Transcode = b
	})

	mediafilelabel := widget.NewLabel(lang.L("Media File") + ":")
	subsfilelabel := widget.NewLabel(lang.L("Subtitles") + ":")
	devicelabel := widget.NewLabel(lang.L("Select Device") + ":")
	deviceHeader := container.NewBorder(nil, nil, devicelabel, container.NewHBox(discoveryStatus, refreshDevices))

	s.PlayPause = playpause
	s.Stop = stop
	s.MuteUnmute = muteunmute
	s.ExternalMediaURL = externalmedia
	s.TranscodeCheckBox = transcode
	s.LoopSelectedCheck = medialoop
	s.MediaText = mfiletext
	s.SubsText = sfiletext
	s.DeviceList = list
	s.SlideBar = sliderBar
	s.CurrentPos = curPos
	s.EndPos = endPos

	curPos.Set("00:00:00")
	endPos.Set("00:00:00")
	sliderArea := container.NewBorder(nil, nil, widget.NewLabelWithData(curPos), widget.NewLabelWithData(endPos), sliderBar)
	playbackStatus := widget.NewLabel("")
	playbackStatus.Alignment = fyne.TextAlignCenter
	playbackStatus.Truncation = fyne.TextTruncateEllipsis
	s.playbackStatus = playbackStatus
	s.timeline = sliderArea
	songLabel := newRadioSongLabel(s)
	songLabel.Importance = widget.MediumImportance
	songLabel.Alignment = fyne.TextAlignCenter
	songLabel.TextStyle.Bold = true
	songLabel.Show()
	radioDetail := widget.NewLabel("")
	radioDetail.Alignment = fyne.TextAlignCenter
	radioDetail.Truncation = fyne.TextTruncateEllipsis
	radioDetail.Importance = widget.LowImportance
	s.radioDetail = radioDetail
	// Tight label insets keep the two radio lines close to one row's height.
	radioRow := container.NewThemeOverride(container.New(layout.NewCustomPaddedVBoxLayout(0), songLabel, radioDetail),
		liveTheme{sizes: map[fyne.ThemeSizeName]float32{theme.SizeNameInnerPadding: 2}})
	radioRow.Hide()
	s.radioRow = radioRow
	// Reserve the two-line radio height so the bar never jumps.
	rowHeight := canvas.NewRectangle(color.Transparent)
	rowHeight.SetMinSize(fyne.NewSize(0, radioRow.MinSize().Height))
	centered := func(o fyne.CanvasObject) fyne.CanvasObject {
		return container.NewVBox(layout.NewSpacer(), o, layout.NewSpacer())
	}
	s.volumeControls = []fyne.Disableable{volumedown, muteunmute, volumeup}
	actionbuttons := container.New(&playerButtonsLayout{}, playpause, stop, volumedown, muteunmute, volumeup)

	checklistItems := []fyne.CanvasObject{externalmedia, medialoop}
	if err := utils.CheckFFmpeg(s.ffmpegPath); err == nil {
		checklistItems = append(checklistItems, transcode)
	}
	// Options spread across one row and wrap only when the screen is too narrow.
	checklists := container.New(newSpreadRowLayout(), checklistItems...)
	mediasubsbuttons := container.New(layout.NewGridLayout(2), mfile, sfile)
	radio := newRadioButton(s)
	radio.SetIcon(theme.MediaMusicIcon())
	sfiletextArea := container.NewBorder(nil, nil, nil, clearsubs, subsField)
	mfiletextArea := container.NewBorder(nil, nil, nil, clearmedia, container.NewStack(mfiletext, mediaField))
	viewfilescont := container.New(layout.NewFormLayout(), mediafilelabel, mfiletextArea, subsfilelabel, sfiletextArea)
	sourceScroll := container.NewVScroll(container.NewVBox(mediasubsbuttons, container.NewGridWithColumns(2, newTorrentButton(s), radio), newTorrentControls(s), viewfilescont, checklists))
	deviceArea := container.NewBorder(container.NewPadded(deviceHeader), nil, nil, nil, container.NewStack(list, container.NewPadded(noDevicesHint)))
	player := newPlayerBar(container.NewVBox(container.NewStack(rowHeight, centered(sliderArea), centered(playbackStatus), radioRow), actionbuttons))
	content := container.New(&mobileMainLayout{source: sourceScroll}, newContentSized(sourceScroll), deviceArea, player)

	// Widgets actions
	list.OnSelected = func(id widget.ListItemID) {
		defer s.refreshPlaybackReadiness()
		defer list.Refresh()

		// Discovery reorders rows and reselects the same device; keep its state.
		if data[id].addr == s.selectedDevice.addr && data[id].deviceType == s.selectedDeviceType &&
			(s.selectedDeviceType == devices.DeviceTypeChromecast || s.controlURL != "" || s.connectingDevice != "") {
			return
		}
		s.selectedDevice = data[id]
		s.selectedDeviceType = data[id].deviceType

		// Reset device state when switching, but preserve active Chromecast session
		// so user can still control it while browsing other devices
		currentState := s.getScreenState()
		isActivePlayback := currentState == "Playing" || currentState == "Paused"
		if !isActivePlayback && s.shouldCloseChromecastClientOnSelectionChange() {
			s.chromecastClient.Log().Debug("closing idle Chromecast after device selection", "Method", "DeviceSelection")
			s.chromecastClient.Close(false)
			s.chromecastClient = nil
		}

		switch data[id].deviceType {
		case devices.DeviceTypeDLNA:
			// Play stays disabled until this renderer's URLs arrive.
			s.controlURL, s.eventlURL, s.renderingControlURL, s.connectionManagerURL = "", "", "", ""
			device := data[id]
			s.connectingDevice = device.addr
			go func() {
				t, err := soapcalls.DMRextractor(context.Background(), device.addr)
				fyne.Do(func() {
					if s.selectedDevice.addr != device.addr || s.connectingDevice != device.addr {
						return
					}
					s.connectingDevice = ""
					if err != nil {
						// Unselect so tapping the row retries the lookup.
						s.clearDeviceSelection()
						check(w, err)
						s.refreshPlaybackReadiness()
						return
					}
					s.controlURL = t.AvtransportControlURL
					s.eventlURL = t.AvtransportEventSubURL
					s.renderingControlURL = t.RenderingControlURL
					s.connectionManagerURL = t.ConnectionManagerURL
					if s.tvdata != nil {
						s.tvdata.RenderingControlURL = s.renderingControlURL
					}
					s.refreshPlaybackReadiness()
				})
			}()
		case devices.DeviceTypeChromecast:
			// Clear DLNA-specific state when selecting Chromecast
			s.connectingDevice = ""
			s.controlURL = ""
			s.eventlURL = ""
			s.renderingControlURL = ""
			s.connectionManagerURL = ""
		}
	}

	var mediafileOld fyne.URI
	var mediafileOldText string

	// updateMediaField shows a radio station by name, a typed URL in the
	// entry, or the selected file. Live radio cannot loop.
	updateMediaField := func() {
		station, radio := s.radioStationForURL(mfiletext.Text)
		switch {
		case radio:
			mediafilelabel.SetText(lang.L("Station") + ":")
			mediaField.Set(station.Name, "")
			mediaField.OnTapped = func() { showRadioDialog(s) }
			mfiletext.Hide()
			mediaField.Show()
			medialoop.SetChecked(false)
			medialoop.Disable()
			return
		case externalmedia.Checked:
			mediafilelabel.SetText(lang.L("URL") + ":")
			mediaField.Hide()
			mfiletext.Show()
		default:
			mediafilelabel.SetText(lang.L("Media File") + ":")
			mediaField.Set(mfiletext.Text, lang.L("No media selected"))
			mediaField.OnTapped = func() { mediaAction(s) }
			mfiletext.Hide()
			mediaField.Show()
		}
		medialoop.Enable()
	}
	mfiletext.OnChanged = func(text string) {
		s.invalidateRadioURL(text)
		updateMediaField()
		s.refreshPlaybackReadiness()
	}
	updateMediaField()

	externalmedia.OnChanged = func(b bool) {
		if b {
			// keep old values
			mediafileOld = s.mediafile
			mediafileOldText = s.MediaText.Text
			if torrentMediaSelected(s) {
				mediafileOld = nil
				mediafileOldText = ""
			}

			// Bring back the last URL; clearing first would drop its radio station.
			if torrentMediaSelected(s) {
				cancelTorrent(s)
			}
			s.mediafile = nil
			s.setCurrentArtwork(nil)
			s.mu.RLock()
			urlInput := s.urlInput
			s.mu.RUnlock()
			s.MediaText.SetText(urlInput)
			updateMediaField()
			return
		}

		s.MediaText.SetText(mediafileOldText)
		s.mediafile = mediafileOld
		resolveSelectedMobileArtwork(s, mediafileOld)
		updateMediaField()
	}

	medialoop.OnChanged = func(b bool) {
		s.Medialoop = b
	}

	// Device list auto-refresh
	go func() {
		<-blockGetDevices
		refreshDevList(s, &data, discoveryStatus, noDevicesHint, rescan)
	}()

	// Check mute status for selected device
	go checkMutefunc(s)

	// Keep track of the media progress and reflect that to the slide bar.
	go sliderUpdate(s)

	s.refreshPlaybackReadiness()

	return content
}

func refreshDevList(s *FyneScreen, data *[]devType, discoveryStatus, noDevicesHint *widget.Label, rescan <-chan struct{}) {
	refreshDevices := time.NewTicker(5 * time.Second)
	defer refreshDevices.Stop()

	for {
		select {
		case <-refreshDevices.C:
		case <-rescan:
		}
		datanew, discoveryErr := getDevices()
		if errors.Is(discoveryErr, devices.ErrNoDeviceAvailable) {
			discoveryErr = nil
		}

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
			for _, new := range datanew {
				newAddress, _ := url.Parse(new.addr)
				if newAddress.Host == oldAddress.Host {
					continue outer
				}
			}

			if utils.HostPortIsAlive(oldAddress.Host) {
				datanew = append(datanew, old)
			}
		}

		// Sort devices alphabetically for consistent ordering
		sortDevTypeSlice(datanew)

		// check to see if the new refresh includes
		// one of the already selected devices
		var includes bool
		if selectedAddr != "" {
			u, _ := url.Parse(selectedAddr)
			for _, d := range datanew {
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
			for n, a := range datanew {
				if selectedDeviceAddr == a.addr {
					foundIdx = n
					break
				}
			}
		}

		fyne.DoAndWait(func() {
			*data = datanew
			discoveryStatus.SetText(discoveryStatusText(len(datanew), true, discoveryErr))
			noDevicesHint.Hidden = len(datanew) > 0
			noDevicesHint.Refresh()

			state := s.getScreenState()
			playing := state == "Playing" || state == "Paused"
			switch {
			case clearSelection, foundIdx < 0 && selectedDeviceAddr != "" && !playing:
				// A vanished device must not stay selected without a checked row.
				s.clearDeviceSelection()
			case foundIdx >= 0:
				s.DeviceList.Select(foundIdx)
			default:
				s.DeviceList.UnselectAll()
			}

			s.DeviceList.Refresh()
			s.refreshPlaybackReadiness()
		})
	}
}

func checkMutefunc(s *FyneScreen) {
	checkMute := time.NewTicker(time.Second)

	var checkMuteCounter int
	for range checkMute.C {
		// Chromecast mute state is synced by chromecastStatusWatcher from the
		// status it already polls; a second concurrent GetStatus loop only
		// doubles control traffic and races the library's state updates.
		// Device fields belong to the UI thread; snapshot them there.
		var payload *soapcalls.TVPayload
		var renderingURL string
		fyne.DoAndWait(func() {
			if s.selectedDeviceType == devices.DeviceTypeChromecast || s.renderingControlURL == "" {
				return
			}
			renderingURL = s.renderingControlURL
			payload = renderingPayload(s)
		})
		if payload == nil {
			checkMuteCounter = 0
			continue
		}

		isMuted, err := payload.GetMuteSoapCall()
		if err != nil {
			// Stop trying after 5 failures to get the mute status.
			if checkMuteCounter++; checkMuteCounter == 5 {
				checkMuteCounter = 0
				fyne.Do(func() {
					if s.renderingControlURL == renderingURL {
						s.renderingControlURL = ""
						s.refreshPlaybackStatus()
					}
				})
			}
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
