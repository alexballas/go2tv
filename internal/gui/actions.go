//go:build !(android || ios)
// +build !android,!ios

package gui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"github.com/alexballas/go2tv/internal/devices"
	"github.com/alexballas/go2tv/internal/httphandlers"
	"github.com/alexballas/go2tv/internal/soapcalls"
	"github.com/alexballas/go2tv/internal/urlstreamer"
	"github.com/alexballas/go2tv/internal/utils"
	"github.com/pkg/errors"
	"github.com/skratchdot/open-golang/open"
)

func muteAction(screen *NewScreen) {
	w := screen.Current
	if screen.renderingControlURL == "" {
		check(w, errors.New("please select a device"))
		return
	}

	if screen.MuteUnmute.Icon == theme.VolumeUpIcon() {
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
		check(w, errors.New("could not send mute action"))
		return
	}

	setMuteUnmuteView("Unmute", screen)
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

		mfile := reader.URI().Path()
		absMediaFile, err := filepath.Abs(mfile)
		check(w, err)

		screen.MediaText.Text = filepath.Base(mfile)
		screen.mediafile = absMediaFile

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

	fd.Resize(fyne.NewSize(w.Canvas().Size().Width*1.2, w.Canvas().Size().Height*1.3))
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
		screen.subsfile = absSubtitlesFile
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
	fd.Resize(fyne.NewSize(w.Canvas().Size().Width*1.2, w.Canvas().Size().Height*1.3))

	fd.Show()
}

func playAction(screen *NewScreen) {
	var mediaFile interface{}

	screen.PlayPause.Disable()

	w := screen.Current

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
		screen.httpserver.StopServeFiles()
	}

	if screen.mediafile == "" && screen.MediaText.Text == "" {
		check(w, errors.New("please select a media file or enter a media URL"))
		screen.PlayPause.Enable()
		return
	}

	if screen.controlURL == "" {
		check(w, errors.New("please select a device"))
		screen.PlayPause.Enable()
		return
	}

	whereToListen, err := utils.URLtoListenIPandPort(screen.controlURL)
	check(w, err)
	if err != nil {
		screen.PlayPause.Enable()
		return
	}

	var mediaType string

	if !screen.ExternalMediaURL.Checked {
		mediaType, err = utils.GetMimeDetailsFromFile(screen.mediafile)
		check(w, err)
		if err != nil {
			screen.PlayPause.Enable()
			return
		}
	}

	callbackPath, err := utils.RandomString()
	if err != nil {
		screen.PlayPause.Enable()
		return
	}

	mediaFile = screen.mediafile

	if screen.ExternalMediaURL.Checked {
		// We need to define the screen.mediafile
		// as this is the core item in our structure
		// that define that something is being streammed.
		// We use its value for many checks in our code.
		screen.mediafile = screen.MediaText.Text

		// We're not using any context here. The reason is
		// that when the webserver shuts down it causes the
		// the io.Copy operation to fail with "broken pipe".
		// That's good enough for us since right after that
		// we close the io.ReadCloser.
		mediaFile, err = urlstreamer.StreamURL(context.Background(), screen.MediaText.Text)
		check(screen.Current, err)
		if err != nil {
			screen.PlayPause.Enable()
			return
		}
	}

	screen.tvdata = &soapcalls.TVPayload{
		ControlURL:          screen.controlURL,
		EventURL:            screen.eventlURL,
		RenderingControlURL: screen.renderingControlURL,
		MediaURL:            "http://" + whereToListen + "/" + utils.ConvertFilename(screen.mediafile),
		SubtitlesURL:        "http://" + whereToListen + "/" + utils.ConvertFilename(screen.subsfile),
		CallbackURL:         "http://" + whereToListen + "/" + callbackPath,
		MediaType:           mediaType,
		CurrentTimers:       make(map[string]*time.Timer),
	}

	screen.httpserver = httphandlers.NewServer(whereToListen)
	serverStarted := make(chan struct{})

	// We pass the tvdata here as we need the callback handlers to be able to react
	// to the different media renderer states.
	go func() {
		err := screen.httpserver.ServeFiles(serverStarted, mediaFile, screen.subsfile, screen.tvdata, screen)
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
	screen.mediafile = ""
	screen.MediaText.Refresh()
}

func clearsubsAction(screen *NewScreen) {
	screen.SubsText.Text = ""
	screen.subsfile = ""
	screen.SubsText.Refresh()
}

func previewmedia(screen *NewScreen) {
	w := screen.Current

	if screen.mediafile == "" {
		check(w, errors.New("please select a media file"))
		return
	}

	mediaType, err := utils.GetMimeDetailsFromFile(screen.mediafile)
	check(w, err)

	mediaTypeSlice := strings.Split(mediaType, "/")
	switch mediaTypeSlice[0] {
	case "image":
		img := canvas.NewImageFromFile(screen.mediafile)
		img.FillMode = 1
		imgw := fyne.CurrentApp().NewWindow(filepath.Base(screen.mediafile))
		imgw.SetContent(img)
		imgw.Resize(fyne.NewSize(800, 600))
		imgw.CenterOnScreen()
		imgw.Show()
	default:
		err := open.Run(screen.mediafile)
		check(w, err)
	}
}

func stopAction(screen *NewScreen) {
	w := screen.Current

	screen.PlayPause.Enable()

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

func getDevices(delay int) ([]devType, error) {
	deviceList, err := devices.LoadSSDPservices(delay)
	if err != nil {
		return nil, fmt.Errorf("getDevices error: %w", err)
	}
	// We loop through this map twice as we need to maintain
	// the correct order.
	keys := make([]string, 0)
	for k := range deviceList {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	guiDeviceList := make([]devType, 0)
	for _, k := range keys {
		guiDeviceList = append(guiDeviceList, devType{k, deviceList[k]})
	}

	return guiDeviceList, nil
}

func volumeAction(screen *NewScreen, up bool) {
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

	currentVolume, err := screen.tvdata.GetVolumeSoapCall()
	if err != nil {
		check(w, errors.New("could not get the volume levels"))
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
		check(w, errors.New("could not send volume action"))
	}

}
