package gui

import (
	"net/url"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/alexballas/go2tv/internal/soapcalls"
	"github.com/alexballas/go2tv/internal/utils"
)

func mainWindow(s *NewScreen) fyne.CanvasObject {
	w := s.Current

	refreshDevices := time.NewTicker(5 * time.Second)
	checkMute := time.NewTicker(1 * time.Second)

	list := new(widget.List)

	data := make([]devType, 0)

	w.Canvas().SetOnTypedKey(func(k *fyne.KeyEvent) {
		if k.Name == "Space" || k.Name == "P" {
			switch s.State {
			case "Playing":
				go pauseAction(s)
			case "Paused":
				go playAction(s)
			}
		}

		if k.Name == "S" {
			go stopAction(s)
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
	subsfilelabel := canvas.NewText("Subtitles:", theme.ForegroundColor())
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
			s.selectedDevice = data[id]
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
		s.Medialoop = b
	}

	nextmedia.OnChanged = func(b bool) {
		s.NextMedia = b
	}

	// Device list auto-refresh
	go func() {
		for range refreshDevices.C {
			datanew, _ := getDevices(2)
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

			data = datanew

			if !includes {
				if utils.HostPortIsAlive(u.Host) {
					data = append(data, s.selectedDevice)
				} else {
					s.controlURL = ""
					s.DeviceList.UnselectAll()
				}
			}
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

	return content
}
