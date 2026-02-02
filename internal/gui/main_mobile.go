//go:build android || ios

package gui

import (
	"context"
	"errors"
	"net/url"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

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
		o.(*fyne.Container).Objects[1].(*widget.Label).SetText((*dd)[i].name)
	}

	list.ExtendBaseWidget(list)
	return list
}

func mainWindow(s *FyneScreen) fyne.CanvasObject {
	w := s.Current
	var data []devType
	list := newDeviceList(&data)

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

	mfile := widget.NewButton(lang.L("Select Media File"), func() {
		mediaAction(s)
	})

	mfiletext.Disable()

	sfile := widget.NewButton(lang.L("Select Subtitles File"), func() {
		subsAction(s)
	})

	sfiletext.Disable()

	playpause := widget.NewButtonWithIcon(lang.L("Play"), theme.MediaPlayIcon(), func() {
		playAction(s)
	})

	stop := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), func() {
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

	externalmedia := widget.NewCheck(lang.L("Media from URL"), func(b bool) {})
	medialoop := widget.NewCheck(lang.L("Loop Selected"), func(b bool) {})

	mediafilelabel := widget.NewLabel(lang.L("Media File") + ":")
	subsfilelabel := widget.NewLabel(lang.L("Subtitles") + ":")
	devicelabel := widget.NewLabel(lang.L("Select Device") + ":")

	s.PlayPause = playpause
	s.Stop = stop
	s.MuteUnmute = muteunmute
	s.ExternalMediaURL = externalmedia
	s.MediaText = mfiletext
	s.SubsText = sfiletext
	s.DeviceList = list

	actionbuttons := container.New(&mainButtonsLayout{buttonHeight: 1.5, buttonPadding: theme.Padding()},
		playpause,
		volumedown,
		muteunmute,
		volumeup,
		stop)

	checklists := container.NewHBox(externalmedia, medialoop)
	mediasubsbuttons := container.New(layout.NewGridLayout(2), mfile, sfile)
	sfiletextArea := container.New(layout.NewBorderLayout(nil, nil, nil, clearsubs), clearsubs, sfiletext)
	mfiletextArea := container.New(layout.NewBorderLayout(nil, nil, nil, clearmedia), clearmedia, mfiletext)
	viewfilescont := container.New(layout.NewFormLayout(), mediafilelabel, mfiletextArea, subsfilelabel, sfiletextArea)
	buttons := container.NewVBox(mediasubsbuttons, viewfilescont, checklists, actionbuttons, container.NewPadded(devicelabel))
	content := container.New(layout.NewBorderLayout(buttons, nil, nil, nil), buttons, list)

	// Widgets actions
	list.OnSelected = func(id widget.ListItemID) {
		playpause.Enable()

		s.selectedDevice = data[id]
		s.selectedDeviceType = data[id].deviceType

		// Reset device state when switching, but preserve active Chromecast session
		// so user can still control it while browsing other devices
		currentState := s.getScreenState()
		isActivePlayback := currentState == "Playing" || currentState == "Paused"
		if s.chromecastClient != nil && !isActivePlayback {
			s.chromecastClient.Close(false)
			s.chromecastClient = nil
		}

		switch data[id].deviceType {
		case devices.DeviceTypeDLNA:
			t, err := soapcalls.DMRextractor(context.Background(), data[id].addr)
			check(w, err)
			if err == nil {
				s.controlURL = t.AvtransportControlURL
				s.eventlURL = t.AvtransportEventSubURL
				s.renderingControlURL = t.RenderingControlURL
				s.connectionManagerURL = t.ConnectionManagerURL
				if s.tvdata != nil {
					s.tvdata.RenderingControlURL = s.renderingControlURL
				}
			}
		case devices.DeviceTypeChromecast:
			// Clear DLNA-specific state when selecting Chromecast
			s.controlURL = ""
			s.eventlURL = ""
			s.renderingControlURL = ""
			s.connectionManagerURL = ""
		}
	}

	var mediafileOld fyne.URI
	var mediafileOldText string

	externalmedia.OnChanged = func(b bool) {
		if b {
			mfile.Disable()

			// rename the label
			mediafilelabel.Text = lang.L("URL") + ":"
			mediafilelabel.Refresh()

			// keep old values
			mediafileOld = s.mediafile
			mediafileOldText = s.MediaText.Text

			// Clear the Media Text Area
			clearmediaAction(s)

			// Set some Media text defaults
			// to indicate that we're expecting a URL
			mfiletext.SetPlaceHolder(lang.L("Enter URL here"))
			mfiletext.Enable()
			return
		}

		medialoop.Enable()
		mfile.Enable()
		mediafilelabel.Text = lang.L("Media File") + ":"
		mfiletext.SetPlaceHolder("")
		s.MediaText.Text = mediafileOldText
		s.mediafile = mediafileOld
		mediafilelabel.Refresh()
		mfiletext.Disable()
	}

	medialoop.OnChanged = func(b bool) {
		s.Medialoop = b
	}

	// Device list auto-refresh
	go func() {
		<-blockGetDevices
		refreshDevList(s, &data)
	}()

	// Check mute status for selected device
	go checkMutefunc(s)

	return content
}

func refreshDevList(s *FyneScreen, data *[]devType) {
	refreshDevices := time.NewTicker(5 * time.Second)

	w := s.Current

	_, err := getDevices(2)
	if err != nil && !errors.Is(err, devices.ErrNoDeviceAvailable) {
		check(w, err)
	}

	for range refreshDevices.C {
		datanew, _ := getDevices(2)

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
	checkMute := time.NewTicker(1 * time.Second)

	var checkMuteCounter int
	for range checkMute.C {
		// Handle Chromecast mute status
		if s.selectedDeviceType == devices.DeviceTypeChromecast {
			if s.chromecastClient == nil || !s.chromecastClient.IsConnected() {
				continue
			}
			status, err := s.chromecastClient.GetStatus()
			if err != nil {
				continue
			}
			if status.Muted {
				setMuteUnmuteView("Unmute", s)
			} else {
				setMuteUnmuteView("Mute", s)
			}
			continue
		}

		// Handle DLNA mute status
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
