//go:build !(android || ios)
// +build !android,!ios

package gui

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
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
	"github.com/alexballas/go2tv/devices"
	"github.com/alexballas/go2tv/httphandlers"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/alexballas/go2tv/soapcalls/utils"
	"github.com/pkg/errors"
	"github.com/skratchdot/open-golang/open"
)

func muteAction(screen *NewScreen) {
	if screen.renderingControlURL == "" {
		check(screen, errors.New("please select a device"))
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
		check(screen, errors.New("could not send mute action"))
		return
	}

	setMuteUnmuteView("Unmute", screen)
}

func unmuteAction(screen *NewScreen) {
	if screen.renderingControlURL == "" {
		check(screen, errors.New("please select a device"))
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
		check(screen, errors.New("could not send mute action"))
		return
	}

	setMuteUnmuteView("Mute", screen)
}

func selectMediaFile(screen *NewScreen, f fyne.URI) {
	mfile := f.Path()
	absMediaFile, err := filepath.Abs(mfile)
	check(screen, err)
	if err != nil {
		return
	}

	screen.SelectInternalSubs.ClearSelected()
	screen.ExternalMediaURL.SetChecked(false)

	screen.MediaText.Text = filepath.Base(mfile)
	screen.mediafile = absMediaFile

	if !screen.CustomSubsCheck.Checked {
		autoSelectNextSubs(absMediaFile, screen)
	}

	// Remember the last file location.
	screen.currentmfolder = filepath.Dir(absMediaFile)

	screen.MediaText.Refresh()

	subs, err := utils.GetSubs(screen.ffmpegPath, absMediaFile)
	if err != nil {
		screen.SelectInternalSubs.Options = []string{}
		screen.SelectInternalSubs.PlaceHolder = "No Embedded Subs"
		screen.SelectInternalSubs.ClearSelected()
		screen.SelectInternalSubs.Disable()
		return
	}

	screen.SelectInternalSubs.Options = subs
	screen.SelectInternalSubs.PlaceHolder = "Embedded Subs"

	screen.SelectInternalSubs.Enable()
}

func selectSubsFile(screen *NewScreen, f fyne.URI) {
	sfile := f.Path()
	absSubtitlesFile, err := filepath.Abs(sfile)
	check(screen, err)
	if err != nil {
		return
	}

	screen.SelectInternalSubs.ClearSelected()

	screen.SubsText.Text = filepath.Base(sfile)
	screen.subsfile = absSubtitlesFile
	screen.SubsText.Refresh()
}

func mediaAction(screen *NewScreen) {
	w := screen.Current
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		check(screen, err)

		if reader == nil {
			return
		}
		defer reader.Close()

		selectMediaFile(screen, reader.URI())
	}, w)

	fd.SetFilter(storage.NewExtensionFileFilter(screen.mediaFormats))

	if screen.currentmfolder != "" {
		mfileURI := storage.NewFileURI(screen.currentmfolder)
		mfileLister, err := storage.ListerForURI(mfileURI)
		check(screen, err)
		fd.SetLocation(mfileLister)
	}

	fd.Resize(fyne.NewSize(w.Canvas().Size().Width*1.2, w.Canvas().Size().Height*1.3))
	fd.Show()
}

func subsAction(screen *NewScreen) {
	w := screen.Current
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		check(screen, err)

		if reader == nil {
			return
		}
		defer reader.Close()

		selectSubsFile(screen, reader.URI())
	}, w)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".srt"}))

	if screen.currentmfolder != "" {
		mfileURI := storage.NewFileURI(screen.currentmfolder)
		mfileLister, err := storage.ListerForURI(mfileURI)
		check(screen, err)
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

	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}

	ctx, cancelEnablePlay := context.WithTimeout(context.Background(), 3*time.Second)
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
		case "PAUSED_PLAYBACK":
			setPlayPauseView("Play", screen)
			screen.updateScreenState("Paused")
		}
	}()

	currentState := screen.getScreenState()

	if currentState == "Paused" {
		err := screen.tvdata.SendtoTV("Play")
		check(screen, err)
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

	if screen.mediafile == "" && screen.MediaText.Text == "" {
		check(screen, errors.New("please select a media file or enter a media URL"))
		startAfreshPlayButton(screen)
		return
	}

	if screen.controlURL == "" {
		check(screen, errors.New("please select a device"))
		startAfreshPlayButton(screen)
		return
	}

	whereToListen, err := utils.URLtoListenIPandPort(screen.controlURL)
	check(screen, err)
	if err != nil {
		startAfreshPlayButton(screen)
		return
	}

	var mediaType string
	var isSeek bool

	if !screen.ExternalMediaURL.Checked {
		mfile, err := os.Open(screen.mediafile)
		check(screen, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		mediaType, err = utils.GetMimeDetailsFromFile(mfile)
		check(screen, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		if !screen.Transcode {
			isSeek = true
		}
	}

	callbackPath, err := utils.RandomString()
	if err != nil {
		startAfreshPlayButton(screen)
		return
	}

	mediaFile = screen.mediafile

	if screen.ExternalMediaURL.Checked {
		// We need to define the screen.mediafile
		// as this is the core item in our structure
		// that defines that something is being streamed.
		// We use its value for many checks in our code.
		screen.mediafile = screen.MediaText.Text

		// We're not using any context here. The reason is
		// that when the webserver shuts down it causes the
		// the io.Copy operation to fail with "broken pipe".
		// That's good enough for us since right after that
		// we close the io.ReadCloser.
		mediaURL, err := utils.StreamURL(context.Background(), screen.MediaText.Text)
		check(screen, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		mediaURLinfo, err := utils.StreamURL(context.Background(), screen.MediaText.Text)
		check(screen, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		mediaType, err = utils.GetMimeDetailsFromStream(mediaURLinfo)
		check(screen, err)
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

	if screen.SelectInternalSubs.Selected != "" {
		for n, opt := range screen.SelectInternalSubs.Options {
			if opt == screen.SelectInternalSubs.Selected {
				screen.PlayPause.Text = "Extracting Subtitles"
				screen.PlayPause.Refresh()
				tempSubsPath, err := utils.ExtractSub(screen.ffmpegPath, n, screen.mediafile)
				screen.PlayPause.Text = "Play"
				screen.PlayPause.Refresh()
				if err != nil {
					break
				}

				screen.tempFiles = append(screen.tempFiles, tempSubsPath)
				screen.subsfile = tempSubsPath
			}
		}
	}

	screen.tvdata = &soapcalls.TVPayload{
		ControlURL:                  screen.controlURL,
		EventURL:                    screen.eventlURL,
		RenderingControlURL:         screen.renderingControlURL,
		ConnectionManagerURL:        screen.connectionManagerURL,
		MediaURL:                    "http://" + whereToListen + "/" + utils.ConvertFilename(screen.mediafile),
		SubtitlesURL:                "http://" + whereToListen + "/" + utils.ConvertFilename(screen.subsfile),
		CallbackURL:                 "http://" + whereToListen + "/" + callbackPath,
		MediaType:                   mediaType,
		MediaPath:                   screen.mediafile,
		CurrentTimers:               make(map[string]*time.Timer),
		MediaRenderersStates:        make(map[string]*soapcalls.States),
		InitialMediaRenderersStates: make(map[string]bool),
		Transcode:                   screen.Transcode,
		Seekable:                    isSeek,
		LogOutput:                   screen.Debug,
		FFmpegPath:                  screen.ffmpegPath,
	}

	screen.httpserver = httphandlers.NewServer(whereToListen)
	serverStarted := make(chan error)
	serverStoppedCTX, serverCTXStop := context.WithCancel(context.Background())
	screen.serverStopCTX = serverStoppedCTX

	// We pass the tvdata here as we need the callback handlers to be able to react
	// to the different media renderer states.
	go func() {
		screen.httpserver.StartServer(serverStarted, mediaFile, screen.subsfile, screen.tvdata, screen)
		serverCTXStop()
	}()

	// Wait for the HTTP server to properly initialize.
	err = <-serverStarted
	check(screen, err)

	err = screen.tvdata.SendtoTV("Play1")
	check(screen, err)
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

	gaplessOption := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")
	if screen.NextMediaCheck.Checked && gaplessOption == "Enabled" {
		newTVPayload, err := queueNext(screen, false)
		if err != nil {
			stopAction(screen)
		}

		if screen.GaplessMediaWatcher == nil {
			screen.GaplessMediaWatcher = gaplessMediaWatcher
			go screen.GaplessMediaWatcher(serverStoppedCTX, screen, newTVPayload)
		}
	}

}

func startAfreshPlayButton(screen *NewScreen) {
	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}

	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")
}

func gaplessMediaWatcher(ctx context.Context, screen *NewScreen, payload *soapcalls.TVPayload) {
	t := time.NewTicker(1 * time.Second)
out:
	for {
		select {
		case <-t.C:
			gaplessOption := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")
			nextURI, _ := payload.Gapless()

			if nextURI == "NOT_IMPLEMENTED" || gaplessOption == "Disabled" {
				screen.GaplessMediaWatcher = nil
				break out
			}

			if screen.NextMediaCheck.Checked {
				// If we change the current folder of media files we need to ensure
				// that the next song is going to be requeued correctly.
				next, _ := getNextMedia(screen)
				if path.Base(nextURI) == utils.ConvertFilename(next) {
					continue
				}

				if nextURI == "" {
					// No need to check for the error as this is something
					// that we did in previous steps in our workflow
					mPath, _ := url.Parse(screen.tvdata.MediaURL)
					sPath, _ := url.Parse(screen.tvdata.SubtitlesURL)

					// Make sure we clean up after ourselves and avoid
					// leaving any dangling handlers. Given the nextURI is ""
					// we know that the previously playing media entry was
					// replaced by the one in the NextURI entry.
					screen.httpserver.RemoveHandler(mPath.Path)
					screen.httpserver.RemoveHandler(sPath.Path)

					screen.MediaText.Text, screen.mediafile = getNextMedia(screen)
					screen.MediaText.Refresh()

					if !screen.CustomSubsCheck.Checked {
						autoSelectNextSubs(screen.mediafile, screen)
					}
				}

				newTVPayload, err := queueNext(screen, false)
				if err != nil {
					stopAction(screen)
				}
				screen.tvdata = payload
				payload = newTVPayload
			}
		case <-ctx.Done():
			t.Stop()
			screen.GaplessMediaWatcher = nil
			break out
		}
	}
}

func pauseAction(screen *NewScreen) {
	err := screen.tvdata.SendtoTV("Pause")
	check(screen, err)
}

func clearmediaAction(screen *NewScreen) {
	screen.MediaText.Text = ""
	screen.mediafile = ""
	screen.MediaText.Refresh()
	screen.SelectInternalSubs.Options = []string{}
	screen.SelectInternalSubs.PlaceHolder = "No Embedded Subs"
	screen.SelectInternalSubs.ClearSelected()
	screen.SelectInternalSubs.Disable()
}

func clearsubsAction(screen *NewScreen) {
	screen.SelectInternalSubs.ClearSelected()
	screen.SubsText.Text = ""
	screen.subsfile = ""
	screen.SubsText.Refresh()
}

func skipNextAction(screen *NewScreen) {
	if screen.controlURL == "" {
		check(screen, errors.New("please select a device"))
		return
	}

	if screen.mediafile == "" {
		check(screen, errors.New("please select a media file"))
		return
	}

	name, nextMediaPath := getNextMedia(screen)
	screen.MediaText.Text = name
	screen.mediafile = nextMediaPath
	screen.MediaText.Refresh()

	if !screen.CustomSubsCheck.Checked {
		autoSelectNextSubs(screen.mediafile, screen)
	}

	stopAction(screen)

	playAction(screen)
}

func previewmedia(screen *NewScreen) {
	if screen.mediafile == "" {
		check(screen, errors.New("please select a media file"))
		return
	}

	mfile, err := os.Open(screen.mediafile)
	check(screen, err)
	if err != nil {
		return
	}

	mediaType, err := utils.GetMimeDetailsFromFile(mfile)
	check(screen, err)
	if err != nil {
		return
	}

	mediaTypeSlice := strings.Split(mediaType, "/")
	switch mediaTypeSlice[0] {
	case "image":
		img := canvas.NewImageFromFile(screen.mediafile)
		img.FillMode = canvas.ImageFillContain
		img.ScaleMode = canvas.ImageScaleFastest
		imgw := fyne.CurrentApp().NewWindow(filepath.Base(screen.mediafile))
		imgw.SetContent(img)
		imgw.Resize(fyne.NewSize(800, 600))
		imgw.CenterOnScreen()
		imgw.Show()
	default:
		err := open.Run(screen.mediafile)
		check(screen, err)
	}
}

func stopAction(screen *NewScreen) {
	screen.PlayPause.Enable()

	if screen.tvdata == nil || screen.tvdata.ControlURL == "" {
		return
	}

	_ = screen.tvdata.SendtoTV("Stop")

	screen.httpserver.StopServer()
	screen.tvdata = nil
	// In theory, we should expect an emit message
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
	if screen.renderingControlURL == "" {
		check(screen, errors.New("please select a device"))
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
		check(screen, errors.New("could not get the volume levels"))
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
		check(screen, errors.New("could not send volume action"))
	}
}

func queueNext(screen *NewScreen, clear bool) (*soapcalls.TVPayload, error) {
	if screen.tvdata == nil {
		return nil, errors.New("queueNext, nil tvdata")
	}

	if clear {
		if err := screen.tvdata.SendtoTV("ClearQueue"); err != nil {
			return nil, err
		}

		return nil, nil
	}

	fname, fpath := getNextMedia(screen)
	_, spath := getNextPossibleSubs(fname)

	var mediaType string
	var isSeek bool

	mfile, err := os.Open(fpath)
	if err != nil {
		return nil, err
	}

	mediaType, err = utils.GetMimeDetailsFromFile(mfile)
	if err != nil {
		return nil, err
	}

	if !screen.Transcode {
		isSeek = true
	}

	var mediaFile interface{} = fpath
	oldMediaURL, err := url.Parse(screen.tvdata.MediaURL)
	if err != nil {
		return nil, err
	}

	oldSubsURL, err := url.Parse(screen.tvdata.SubtitlesURL)
	if err != nil {
		return nil, err
	}

	nextTvData := &soapcalls.TVPayload{
		ControlURL:                  screen.controlURL,
		EventURL:                    screen.eventlURL,
		RenderingControlURL:         screen.renderingControlURL,
		ConnectionManagerURL:        screen.connectionManagerURL,
		MediaURL:                    "http://" + oldMediaURL.Host + "/" + utils.ConvertFilename(fname),
		SubtitlesURL:                "http://" + oldSubsURL.Host + "/" + utils.ConvertFilename(spath),
		CallbackURL:                 screen.tvdata.CallbackURL,
		MediaType:                   mediaType,
		MediaPath:                   screen.mediafile,
		CurrentTimers:               make(map[string]*time.Timer),
		MediaRenderersStates:        make(map[string]*soapcalls.States),
		InitialMediaRenderersStates: make(map[string]bool),
		Transcode:                   screen.Transcode,
		Seekable:                    isSeek,
		LogOutput:                   screen.Debug,
	}

	//screen.httpNexterver.StartServer(serverStarted, mediaFile, spath, nextTvData, screen)
	mURL, err := url.Parse(nextTvData.MediaURL)
	if err != nil {
		return nil, err
	}

	sURL, err := url.Parse(nextTvData.SubtitlesURL)
	if err != nil {
		return nil, err
	}

	screen.httpserver.AddHandler(mURL.Path, nextTvData, mediaFile)
	screen.httpserver.AddHandler(sURL.Path, nil, spath)

	if err := nextTvData.SendtoTV("Queue"); err != nil {
		return nil, err
	}

	return nextTvData, nil
}
