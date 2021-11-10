package gui

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"github.com/alexballas/go2tv/internal/devices"
	"github.com/alexballas/go2tv/internal/httphandlers"
	"github.com/alexballas/go2tv/internal/soapcalls"
	"github.com/alexballas/go2tv/internal/utils"
	"github.com/pkg/errors"
)

func muteAction(screen *NewScreen) {
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

	if err := screen.tvdata.SetMuteSoapCall("1"); err != nil {
		check(w, errors.New("could not send mute action"))
		return
	}
	screen.Unmute.Show()
	screen.Mute.Hide()
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
	screen.Unmute.Hide()
	screen.Mute.Show()
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
		screen.mediafile = filestruct{
			abs:        absMediaFile,
			urlEncoded: utils.ConvertFilename(absMediaFile),
		}

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

	fd.Resize(fyne.NewSize(w.Canvas().Size().Width*1.4, w.Canvas().Size().Height*1.3))
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
		screen.subsfile = filestruct{
			abs:        absSubtitlesFile,
			urlEncoded: utils.ConvertFilename(absSubtitlesFile),
		}
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
	fd.Resize(fyne.NewSize(w.Canvas().Size().Width*1.4, w.Canvas().Size().Height*1.3))

	fd.Show()
}

func playAction(screen *NewScreen) {
	w := screen.Current
	screen.Play.Disable()
	screen.Play.Refresh()

	if screen.State == "Paused" {
		err := screen.tvdata.SendtoTV("Play")
		check(w, err)
		return
	}
	if screen.mediafile.urlEncoded == "" {
		check(w, errors.New("please select a media file"))
		screen.Play.Enable()
		return
	}
	if screen.controlURL == "" {
		check(w, errors.New("please select a device"))
		screen.Play.Enable()
		return
	}

	if screen.tvdata == nil {
		stopAction(screen)
	}

	whereToListen, err := utils.URLtoListenIPandPort(screen.controlURL)
	check(w, err)
	if err != nil {
		return
	}

	mediaType, err := utils.GetMimeDetails(screen.mediafile.abs)
	check(w, err)
	if err != nil {
		return
	}

	screen.tvdata = &soapcalls.TVPayload{
		ControlURL:          screen.controlURL,
		EventURL:            screen.eventlURL,
		RenderingControlURL: screen.renderingControlURL,
		MediaURL:            "http://" + whereToListen + "/" + screen.mediafile.urlEncoded,
		SubtitlesURL:        "http://" + whereToListen + "/" + screen.subsfile.urlEncoded,
		CallbackURL:         "http://" + whereToListen + "/callback",
		MediaType:           mediaType,
		CurrentTimers:       make(map[string]*time.Timer),
	}

	screen.httpserver = httphandlers.NewServer(whereToListen)
	serverStarted := make(chan struct{})

	// We pass the tvdata here as we need the callback handlers to be able to react
	// to the different media renderer states.
	go func() {
		err := screen.httpserver.ServeFiles(serverStarted, screen.mediafile.abs, screen.subsfile.abs, screen.tvdata, screen)
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
			screen.DeviceList.Refresh()
		}
		screen.controlURL = ""
		stopAction(screen)
	}
}

func pauseAction(screen *NewScreen) {
	w := screen.Current
	screen.Pause.Disable()
	screen.Pause.Refresh()

	err := screen.tvdata.SendtoTV("Pause")
	check(w, err)
}

func clearmediaAction(screen *NewScreen) {
	screen.MediaText.Text = ""
	screen.mediafile.urlEncoded = ""
	screen.MediaText.Refresh()
}

func clearsubsAction(screen *NewScreen) {
	screen.SubsText.Text = ""
	screen.subsfile.urlEncoded = ""
	screen.SubsText.Refresh()
}

func stopAction(screen *NewScreen) {
	w := screen.Current

	screen.Play.Enable()
	screen.Pause.Enable()

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

func getDevices(delay int) (dev []devType, err error) {
	if err := devices.LoadSSDPservices(delay); err != nil {
		return nil, fmt.Errorf("getDevices error: %w", err)
	}
	// We loop through this map twice as we need to maintain
	// the correct order.
	keys := make([]string, 0)
	for k := range devices.Devices {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	guiDeviceList := make([]devType, 0)
	for _, k := range keys {
		guiDeviceList = append(guiDeviceList, devType{k, devices.Devices[k]})
	}

	return guiDeviceList, nil
}
