//go:build android || ios
// +build android ios

package gui

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"github.com/alexballas/go2tv/devices"
	"github.com/alexballas/go2tv/httphandlers"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/alexballas/go2tv/soapcalls/utils"
	"github.com/pkg/errors"
)

func muteAction(screen *NewScreen) {
	w := screen.Current
	if screen.renderingControlURL == "" {
		check(w, errors.New(lang.L("please select a device")))
		return
	}

	if screen.MuteUnmute.Icon == theme.VolumeMuteIcon() {
		unmuteAction(screen)
		return
	}

	if screen.tvdata == nil {
		// If tvdata is nil, we just need to set RenderingControlURL if we want
		// to control the sound. We should still rely on the play action to properly
		// populate our tvdata type.
		screen.tvdata = &soapcalls.TVPayload{RenderingControlURL: screen.renderingControlURL}
	}

	if err := screen.tvdata.SetMuteSoapCall("1"); err != nil {
		check(w, errors.New(lang.L("could not send mute action")))
		return
	}

	setMuteUnmuteView("Unmute", screen)
}

func unmuteAction(screen *NewScreen) {
	w := screen.Current

	if screen.renderingControlURL == "" {
		check(w, errors.New(lang.L("please select a device")))
		return
	}

	if screen.tvdata == nil {
		// If tvdata is nil, we just need to set RenderingControlURL if we want
		// to control the sound. We should still rely on the play action to properly
		// populate our tvdata type.
		screen.tvdata = &soapcalls.TVPayload{RenderingControlURL: screen.renderingControlURL}
	}

	// isMuted, _ := screen.tvdata.GetMuteSoapCall()
	if err := screen.tvdata.SetMuteSoapCall("0"); err != nil {
		check(w, errors.New(lang.L("could not send mute action")))
		return
	}

	setMuteUnmuteView("Mute", screen)
}

func mediaAction(screen *NewScreen) {
	w := screen.Current
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		check(w, err)

		if reader == nil {
			return
		}

		defer reader.Close()

		screen.MediaText.Text = reader.URI().Name()
		screen.mediafile = reader.URI()

		screen.MediaText.Refresh()
	}, w)

	fd.SetFilter(storage.NewExtensionFileFilter(screen.mediaFormats))

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

		check(w, err)
		if err != nil {
			return
		}

		screen.SubsText.Text = reader.URI().Name()
		screen.subsfile = reader.URI()
		screen.SubsText.Refresh()
	}, w)

	fd.SetFilter(storage.NewExtensionFileFilter([]string{".srt"}))

	fd.Show()
}

func playAction(screen *NewScreen) {
	var mediaFile, subsFile interface{}
	w := screen.Current

	screen.PlayPause.Disable()

	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}

	ctx, cancelEnablePlay := context.WithTimeout(context.Background(), 5*time.Second)
	screen.cancelEnablePlay = cancelEnablePlay

	go func() {
		<-ctx.Done()

		defer func() { screen.cancelEnablePlay = nil }()

		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}

		out, err := screen.tvdata.GetTransportInfo()
		if err != nil {
			return
		}

		switch out[0] {
		case "PLAYING":
			setPlayPauseView("Pause", screen)
			screen.updateScreenState("Playing")
		case "PAUSED":
			setPlayPauseView("Play", screen)
			screen.updateScreenState("Paused")
		}
	}()

	currentState := screen.getScreenState()

	if currentState == "Paused" {
		err := screen.tvdata.SendtoTV("Play")
		check(w, err)
		return
	}

	if screen.PlayPause.Text == "Pause" {
		pauseAction(screen)
		return
	}

	// With this check we're covering the edge case
	// where we're able to click 'Play' while a media
	// is looping repeatedly and throws an error that
	// it's not supported by our media renderer.
	// Without this check we'd end up spinning more
	// webservers while keeping the old ones open.
	if screen.httpserver != nil {
		screen.httpserver.StopServer()
	}

	if screen.mediafile == nil && screen.MediaText.Text == "" {
		check(w, errors.New(lang.L("please select a media file or enter a media URL")))
		startAfreshPlayButton(screen)
		return
	}

	if screen.controlURL == "" {
		check(w, errors.New(lang.L("please select a device")))
		startAfreshPlayButton(screen)
		return
	}

	whereToListen, err := utils.URLtoListenIPandPort(screen.controlURL)
	check(w, err)
	if err != nil {
		startAfreshPlayButton(screen)
		return
	}

	var mediaType string

	callbackPath, err := utils.RandomString()
	if err != nil {
		startAfreshPlayButton(screen)
		return
	}

	if screen.mediafile != nil {
		mediaURL, err := storage.OpenFileFromURI(screen.mediafile)
		check(screen.Current, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		mediaURLinfo, err := storage.OpenFileFromURI(screen.mediafile)
		check(screen.Current, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		mediaType, err = utils.GetMimeDetailsFromStream(mediaURLinfo)
		check(w, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		mediaFile = mediaURL
		if strings.Contains(mediaType, "image") {
			readerToBytes, err := io.ReadAll(mediaURL)
			mediaURL.Close()
			if err != nil {
				startAfreshPlayButton(screen)
				return
			}
			mediaFile = readerToBytes
		}
	}

	if screen.subsfile != nil {
		subsFile, err = storage.OpenFileFromURI(screen.subsfile)
		check(screen.Current, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}
	}

	if screen.ExternalMediaURL.Checked {
		// We're not using any context here. The reason is
		// that when the webserver shuts down it causes the
		// the io.Copy operation to fail with "broken pipe".
		// That's good enough for us since right after that
		// we close the io.ReadCloser.
		mediaURL, err := utils.StreamURL(context.Background(), screen.MediaText.Text)
		check(screen.Current, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		mediaURLinfo, err := utils.StreamURL(context.Background(), screen.MediaText.Text)
		check(screen.Current, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		mediaType, err = utils.GetMimeDetailsFromStream(mediaURLinfo)
		check(w, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		mediaFile = mediaURL
		if strings.Contains(mediaType, "image") {
			readerToBytes, err := io.ReadAll(mediaURL)
			mediaURL.Close()
			if err != nil {
				startAfreshPlayButton(screen)
				return
			}
			mediaFile = readerToBytes
		}
	}

	screen.tvdata = &soapcalls.TVPayload{
		ControlURL:                  screen.controlURL,
		EventURL:                    screen.eventlURL,
		RenderingControlURL:         screen.renderingControlURL,
		ConnectionManagerURL:        screen.connectionManagerURL,
		MediaURL:                    "http://" + whereToListen + "/" + utils.ConvertFilename(screen.MediaText.Text),
		SubtitlesURL:                "http://" + whereToListen + "/" + utils.ConvertFilename(screen.SubsText.Text),
		CallbackURL:                 "http://" + whereToListen + "/" + callbackPath,
		MediaType:                   mediaType,
		CurrentTimers:               make(map[string]*time.Timer),
		MediaRenderersStates:        make(map[string]*soapcalls.States),
		InitialMediaRenderersStates: make(map[string]bool),
	}

	screen.httpserver = httphandlers.NewServer(whereToListen)
	serverStarted := make(chan error)

	// We pass the tvdata here as we need the callback handlers to be able to react
	// to the different media renderer states.
	go func() {
		screen.httpserver.StartServer(serverStarted, mediaFile, subsFile, screen.tvdata, screen)
	}()
	// Wait for the HTTP server to properly initialize.
	err = <-serverStarted
	check(w, err)

	err = screen.tvdata.SendtoTV("Play1")
	check(w, err)
	if err != nil {
		// Something failed when sent Play1 to the TV.
		// Just force the user to re-select a device.
		lsize := screen.DeviceList.Length()
		for i := 0; i <= lsize; i++ {
			screen.DeviceList.Unselect(lsize - 1)
		}
		screen.controlURL = ""
		stopAction(screen)
	}
}

func pauseAction(screen *NewScreen) {
	w := screen.Current

	err := screen.tvdata.SendtoTV("Pause")
	check(w, err)
}

func clearmediaAction(screen *NewScreen) {
	screen.MediaText.Text = ""
	screen.mediafile = nil
	screen.MediaText.Refresh()
}

func clearsubsAction(screen *NewScreen) {
	screen.SubsText.Text = ""
	screen.subsfile = nil
	screen.SubsText.Refresh()
}

func stopAction(screen *NewScreen) {
	screen.PlayPause.Enable()

	if screen.tvdata == nil || screen.tvdata.ControlURL == "" {
		return
	}

	_ = screen.tvdata.SendtoTV("Stop")

	screen.httpserver.StopServer()
	screen.tvdata = nil
	// In theory we should expect an emit message
	// from the media renderer, but there seems
	// to be a race condition that prevents this.
	screen.EmitMsg("Stopped")
}

func getDevices(delay int) ([]devType, error) {
	deviceList, err := devices.LoadSSDPservices(delay)
	if err != nil {
		return nil, fmt.Errorf("getDevices error: %w", err)
	}
	// We loop through this map twice as we need to maintain
	// the correct order.
	var keys []string
	for k := range deviceList {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var guiDeviceList []devType
	for _, k := range keys {
		guiDeviceList = append(guiDeviceList, devType{k, deviceList[k]})
	}

	return guiDeviceList, nil
}

func volumeAction(screen *NewScreen, up bool) {
	w := screen.Current
	if screen.renderingControlURL == "" {
		check(w, errors.New(lang.L("please select a device")))
		return
	}

	if screen.tvdata == nil {
		// If tvdata is nil, we just need to set RenderingControlURL if we want
		// to control the sound. We should still rely on the play action to properly
		// populate our tvdata type.
		screen.tvdata = &soapcalls.TVPayload{RenderingControlURL: screen.renderingControlURL}
	}

	currentVolume, err := screen.tvdata.GetVolumeSoapCall()
	if err != nil {
		check(w, errors.New(lang.L("could not get the volume levels")))
		return
	}

	setVolume := currentVolume - 1

	if up {
		setVolume = currentVolume + 1
	}

	if setVolume < 0 {
		setVolume = 0
	}

	stringVolume := strconv.Itoa(setVolume)

	if err := screen.tvdata.SetVolumeSoapCall(stringVolume); err != nil {
		check(w, errors.New(lang.L("could not send volume action")))
	}
}

func startAfreshPlayButton(screen *NewScreen) {
	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}

	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")
}
