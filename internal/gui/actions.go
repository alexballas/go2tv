//go:build !(android || ios)

package gui

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"github.com/alexballas/go2tv/castprotocol"
	"github.com/alexballas/go2tv/devices"
	"github.com/alexballas/go2tv/httphandlers"
	"github.com/alexballas/go2tv/soapcalls"
	"github.com/alexballas/go2tv/soapcalls/utils"
	"github.com/pkg/errors"
	"github.com/skratchdot/open-golang/open"
)

func muteAction(screen *FyneScreen) {
	// Handle icon toggle (mute -> unmute)
	if screen.MuteUnmute.Icon == theme.VolumeMuteIcon() {
		unmuteAction(screen)
		return
	}

	// Handle Chromecast mute
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		if screen.chromecastClient == nil || !screen.chromecastClient.IsConnected() {
			check(screen, errors.New(lang.L("chromecast not connected")))
			return
		}
		if err := screen.chromecastClient.SetMuted(true); err != nil {
			check(screen, errors.New(lang.L("could not send mute action")))
			return
		}
		setMuteUnmuteView("Unmute", screen)
		return
	}

	// Handle DLNA mute
	if screen.renderingControlURL == "" {
		check(screen, errors.New(lang.L("please select a device")))
		return
	}

	if screen.tvdata == nil {
		// If tvdata is nil, we just need to set RenderingControlURL if we want
		// to control the sound. We should still rely on the play action to properly
		// populate our tvdata type.
		screen.tvdata = &soapcalls.TVPayload{RenderingControlURL: screen.renderingControlURL}
	}

	if err := screen.tvdata.SetMuteSoapCall("1"); err != nil {
		check(screen, errors.New(lang.L("could not send mute action")))
		return
	}

	setMuteUnmuteView("Unmute", screen)
}

func unmuteAction(screen *FyneScreen) {
	// Handle Chromecast unmute
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		if screen.chromecastClient == nil || !screen.chromecastClient.IsConnected() {
			check(screen, errors.New(lang.L("chromecast not connected")))
			return
		}
		if err := screen.chromecastClient.SetMuted(false); err != nil {
			check(screen, errors.New(lang.L("could not send mute action")))
			return
		}
		setMuteUnmuteView("Mute", screen)
		return
	}

	// Handle DLNA unmute
	if screen.renderingControlURL == "" {
		check(screen, errors.New(lang.L("please select a device")))
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
		check(screen, errors.New(lang.L("could not send mute action")))
		return
	}

	setMuteUnmuteView("Mute", screen)
}

func selectMediaFile(screen *FyneScreen, f fyne.URI) {
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
		screen.SelectInternalSubs.PlaceHolder = lang.L("No Embedded Subs")
		screen.SelectInternalSubs.ClearSelected()
		screen.SelectInternalSubs.Disable()
		return
	}

	screen.SelectInternalSubs.Options = subs
	screen.SelectInternalSubs.PlaceHolder = lang.L("Embedded Subs")

	screen.SelectInternalSubs.Enable()
}

func selectSubsFile(screen *FyneScreen, f fyne.URI) {
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

func mediaAction(screen *FyneScreen) {
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

func subsAction(screen *FyneScreen) {
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

func playAction(screen *FyneScreen) {
	var mediaFile any

	fyne.Do(func() {
		screen.PlayPause.Disable()
	})

	// Branch based on device type - MUST be first, before any DLNA-specific logic
	// Chromecast has its own status watcher, doesn't need the DLNA timeout mechanism
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		chromecastPlayAction(screen)
		return
	}

	// DLNA timeout mechanism - re-enable play button if no response after 3 seconds
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

	// DLNA pause/resume handling (tvdata required)
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
		check(screen, errors.New(lang.L("please select a media file or enter a media URL")))
		startAfreshPlayButton(screen)
		return
	}

	if screen.selectedDevice.addr == "" {
		check(screen, errors.New(lang.L("please select a device")))
		startAfreshPlayButton(screen)
		return
	}

	// Continue with existing DLNA logic...
	if screen.controlURL == "" {
		check(screen, errors.New(lang.L("please select a device")))
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
				screen.PlayPause.Text = lang.L("Extracting Subtitles")
				screen.PlayPause.Refresh()
				tempSubsPath, err := utils.ExtractSub(screen.ffmpegPath, n, screen.mediafile)
				screen.PlayPause.Text = lang.L("Play")
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
		FFmpegSeek:                  screen.ffmpegSeek,
		FFmpegSubsPath:              screen.subsfile,
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

// chromecastPlayAction handles playback on Chromecast devices.
// Supports both local files (via internal HTTP server) and external URLs (direct).
func chromecastPlayAction(screen *FyneScreen) {
	// Handle pause/resume if already playing
	if screen.chromecastClient != nil && screen.chromecastClient.IsConnected() {
		currentState := screen.getScreenState()
		if currentState == "Paused" {
			if err := screen.chromecastClient.Play(); err != nil {
				check(screen, err)
				startAfreshPlayButton(screen)
				return
			}
			// Update UI to show Pause button (resume succeeded)
			setPlayPauseView("Pause", screen)
			screen.updateScreenState("Playing")
			return
		}
		if screen.getScreenState() == "Playing" {
			if err := screen.chromecastClient.Pause(); err != nil {
				check(screen, err)
				return
			}
			// Update UI to show Play button (pause succeeded)
			setPlayPauseView("Play", screen)
			screen.updateScreenState("Paused")
			return
		}
	}

	// Validate media file or URL
	if screen.mediafile == "" && screen.MediaText.Text == "" {
		check(screen, errors.New(lang.L("please select a media file or enter a media URL")))
		startAfreshPlayButton(screen)
		return
	}

	transcode := screen.Transcode
	ffmpegSeek := screen.ffmpegSeek

	// Handle internal (embedded) subtitles extraction
	if screen.SelectInternalSubs.Selected != "" {
		for n, opt := range screen.SelectInternalSubs.Options {
			if opt == screen.SelectInternalSubs.Selected {
				screen.PlayPause.Text = lang.L("Extracting Subtitles")
				screen.PlayPause.Refresh()
				tempSubsPath, err := utils.ExtractSub(screen.ffmpegPath, n, screen.mediafile)
				screen.PlayPause.Text = lang.L("Play")
				screen.PlayPause.Refresh()
				if err != nil {
					break
				}

				screen.tempFiles = append(screen.tempFiles, tempSubsPath)
				screen.subsfile = tempSubsPath
			}
		}
	}

	// Reuse existing client if connected (for loop/autoplay), otherwise create new one
	client := screen.chromecastClient
	if client == nil || !client.IsConnected() {
		var err error
		client, err = castprotocol.NewCastClient(screen.selectedDevice.addr)
		if err != nil {
			check(screen, fmt.Errorf("chromecast init: %w", err))
			startAfreshPlayButton(screen)
			return
		}

		if err := client.Connect(); err != nil {
			check(screen, fmt.Errorf("chromecast connect: %w", err))
			startAfreshPlayButton(screen)
			return
		}

		screen.chromecastClient = client
	}

	var mediaURL string
	var mediaType string
	var serverStoppedCTX context.Context

	if screen.ExternalMediaURL.Checked {
		mediaURL = screen.MediaText.Text
		screen.mediafile = mediaURL

		mediaURLinfo, err := utils.StreamURL(context.Background(), mediaURL)
		if err != nil {
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}
		mediaType, err = utils.GetMimeDetailsFromStream(mediaURLinfo)
		mediaURLinfo.Close()
		if err != nil {
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}

		var cancel context.CancelFunc
		serverStoppedCTX, cancel = context.WithCancel(context.Background())
		screen.serverStopCTX = serverStoppedCTX
		go func() { <-serverStoppedCTX.Done(); cancel() }()

	} else {
		// LOCAL FILE: Serve via internal HTTP server
		mfile, err := os.Open(screen.mediafile)
		if err != nil {
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}
		mediaType, err = utils.GetMimeDetailsFromFile(mfile)
		mfile.Close()
		if err != nil {
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}

		whereToListen, err := utils.URLtoListenIPandPort(screen.selectedDevice.addr)
		if err != nil {
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}

		if screen.httpserver != nil {
			screen.httpserver.StopServer()
		}

		screen.httpserver = httphandlers.NewServer(whereToListen)
		serverStarted := make(chan error)
		var serverCTXStop context.CancelFunc
		serverStoppedCTX, serverCTXStop = context.WithCancel(context.Background())
		screen.serverStopCTX = serverStoppedCTX
		screen.cancelServerStop = serverCTXStop

		go func() {
			screen.httpserver.StartSimpleServer(serverStarted, screen.mediafile)
			serverCTXStop()
		}()

		if err := <-serverStarted; err != nil {
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}

		mediaURL = "http://" + whereToListen + "/" + utils.ConvertFilename(screen.mediafile)
	}

	// Handle subtitles
	var subtitleURL string
	if screen.subsfile != "" && !transcode && screen.httpserver != nil {
		// Extract host:port from mediaURL to ensure subtitle uses same server
		mediaURLParsed, err := url.Parse(mediaURL)
		if err == nil && mediaURLParsed.Host != "" {
			ext := strings.ToLower(filepath.Ext(screen.subsfile))
			switch ext {
			case ".srt":
				webvttData, err := utils.ConvertSRTtoWebVTT(screen.subsfile)
				if err != nil {
					check(screen, fmt.Errorf("subtitle conversion: %w", err))
				} else {
					screen.httpserver.AddHandler("/subtitles.vtt", nil, webvttData)
					subtitleURL = "http://" + mediaURLParsed.Host + "/subtitles.vtt"
				}
			case ".vtt":
				screen.httpserver.AddHandler("/subtitles.vtt", nil, screen.subsfile)
				subtitleURL = "http://" + mediaURLParsed.Host + "/subtitles.vtt"
			}
		}
	}

	// Load media and update UI on success
	go func() {
		if err := client.Load(mediaURL, mediaType, ffmpegSeek, subtitleURL); err != nil {
			check(screen, fmt.Errorf("chromecast load: %w", err))
			startAfreshPlayButton(screen)
			return
		}
	}()

	go chromecastStatusWatcher(serverStoppedCTX, screen)
}

// chromecastStatusWatcher polls Chromecast status and updates UI.
// Triggers auto-play next via Fini() when media ends, consistent with DLNA.
func chromecastStatusWatcher(ctx context.Context, screen *FyneScreen) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var mediaStarted bool // Track if media has started (seen BUFFERING or PLAYING)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if screen.chromecastClient == nil || !screen.chromecastClient.IsConnected() {
				return
			}

			status, err := screen.chromecastClient.GetStatus()
			if err != nil {
				continue
			}

			// Update state based on player state
			switch status.PlayerState {
			case "BUFFERING":
				// Media is loading - mark as started but don't change UI yet
				mediaStarted = true
			case "PLAYING":
				mediaStarted = true
				if screen.getScreenState() != "Playing" {
					setPlayPauseView("Pause", screen)
					screen.updateScreenState("Playing")
				}
			case "PAUSED":
				mediaStarted = true
				if screen.getScreenState() != "Paused" {
					setPlayPauseView("Play", screen)
					screen.updateScreenState("Paused")
				}
			case "IDLE":
				// Only treat IDLE as "finished" if media had actually started playing
				// Ignore initial IDLE states while media is loading
				if mediaStarted {
					// Media finished - trigger auto-play next or loop via Fini()
					screen.Fini()

					// Only reset UI if not looping or auto-playing next
					if !screen.Medialoop && !screen.NextMediaCheck.Checked {
						startAfreshPlayButton(screen)
					}
					return
				}
				// If we haven't started yet, just ignore IDLE
			}

			// Update slider position (only if media has started)
			if mediaStarted && !screen.sliderActive && status.Duration > 0 {
				progress := (float64(status.CurrentTime) / float64(status.Duration)) * screen.SlideBar.Max
				fyne.Do(func() {
					screen.SlideBar.SetValue(progress)
				})

				// Update time labels
				current, _ := utils.SecondsToClockTime(int(status.CurrentTime))
				total, _ := utils.SecondsToClockTime(int(status.Duration))
				screen.CurrentPos.Set(current)
				screen.EndPos.Set(total)

				// Fallback: Detect media completion when CurrentTime reaches Duration
				// go-chromecast doesn't always report IDLE when media finishes
				// Using 1.5 second threshold since Chromecast stops updating ~1-2s early
				if status.CurrentTime >= status.Duration-1.5 && status.Duration > 0 {
					screen.Fini()
					// Only reset UI if not looping or auto-playing next
					if !screen.Medialoop && !screen.NextMediaCheck.Checked {
						startAfreshPlayButton(screen)
					}
					return
				}
			}
		}
	}
}

func startAfreshPlayButton(screen *FyneScreen) {
	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}

	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")
}

func gaplessMediaWatcher(ctx context.Context, screen *FyneScreen, payload *soapcalls.TVPayload) {
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
					if screen.tvdata == nil {
						continue
					}

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
					fyne.Do(func() {
						screen.MediaText.Refresh()
					})

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

func pauseAction(screen *FyneScreen) {
	err := screen.tvdata.SendtoTV("Pause")
	check(screen, err)
}

func clearmediaAction(screen *FyneScreen) {
	screen.MediaText.Text = ""
	screen.mediafile = ""
	screen.MediaText.Refresh()
	screen.SelectInternalSubs.Options = []string{}
	screen.SelectInternalSubs.PlaceHolder = lang.L("No Embedded Subs")
	screen.SelectInternalSubs.ClearSelected()
	screen.SelectInternalSubs.Disable()
}

func clearsubsAction(screen *FyneScreen) {
	screen.SelectInternalSubs.ClearSelected()
	screen.SubsText.Text = ""
	screen.subsfile = ""
	screen.SubsText.Refresh()
}

func skipNextAction(screen *FyneScreen) {
	// Check if any device is selected (DLNA uses controlURL, Chromecast uses selectedDevice)
	if screen.controlURL == "" && screen.selectedDeviceType != devices.DeviceTypeChromecast {
		check(screen, errors.New(lang.L("please select a device")))
		return
	}

	if screen.mediafile == "" {
		check(screen, errors.New(lang.L("please select a media file")))
		return
	}

	// Capture old path for handler cleanup
	oldMediaPath := screen.mediafile

	name, nextMediaPath := getNextMedia(screen)
	screen.MediaText.Text = name
	screen.mediafile = nextMediaPath
	screen.MediaText.Refresh()

	if !screen.CustomSubsCheck.Checked {
		autoSelectNextSubs(screen.mediafile, screen)
	}

	// For Chromecast: reuse existing connection for faster skip
	if screen.selectedDeviceType == devices.DeviceTypeChromecast &&
		screen.chromecastClient != nil && screen.chromecastClient.IsConnected() {

		// Get media type
		mfile, err := os.Open(screen.mediafile)
		if err != nil {
			check(screen, err)
			return
		}
		mediaType, err := utils.GetMimeDetailsFromFile(mfile)
		mfile.Close()
		if err != nil {
			check(screen, err)
			return
		}

		// Get subtitle URL if needed (remove old handler first)
		subtitleURL := ""
		screen.httpserver.RemoveHandler("/subtitles.vtt")
		if screen.subsfile != "" {
			ext := strings.ToLower(filepath.Ext(screen.subsfile))
			switch ext {
			case ".srt":
				webvttData, err := utils.ConvertSRTtoWebVTT(screen.subsfile)
				if err == nil {
					screen.httpserver.AddHandler("/subtitles.vtt", nil, webvttData)
					subtitleURL = "http://" + screen.httpserver.GetAddr() + "/subtitles.vtt"
				}
			case ".vtt":
				screen.httpserver.AddHandler("/subtitles.vtt", nil, screen.subsfile)
				subtitleURL = "http://" + screen.httpserver.GetAddr() + "/subtitles.vtt"
			}
		}

		// Remove old media handler and add new one
		screen.httpserver.RemoveHandler("/" + utils.ConvertFilename(oldMediaPath))
		screen.httpserver.AddHandler("/"+utils.ConvertFilename(screen.mediafile), nil, screen.mediafile)

		// Build media URL using the actual HTTP server address
		mediaURL := "http://" + screen.httpserver.GetAddr() + "/" + utils.ConvertFilename(screen.mediafile)

		// Load new media on existing connection (async to avoid blocking)
		go func() {
			if err := screen.chromecastClient.Load(mediaURL, mediaType, 0, subtitleURL); err != nil {
				check(screen, fmt.Errorf("chromecast load: %w", err))
				return
			}
		}()

		return
	}

	// For DLNA or if Chromecast client not ready: use stop+play
	stopAction(screen)
	playAction(screen)
}

func previewmedia(screen *FyneScreen) {
	if screen.mediafile == "" {
		check(screen, errors.New(lang.L("please select a media file")))
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

func stopAction(screen *FyneScreen) {
	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")

	if screen.chromecastClient != nil && screen.chromecastClient.IsConnected() {
		_ = screen.chromecastClient.Stop()
		screen.chromecastClient.Close(false)
		screen.chromecastClient = nil
		if screen.cancelServerStop != nil {
			screen.cancelServerStop()
		}
		return
	}

	if screen.tvdata == nil || screen.tvdata.ControlURL == "" {
		return
	}

	_ = screen.tvdata.SendtoTV("Stop")

	if screen.httpserver != nil {
		screen.httpserver.StopServer()
	}

	screen.tvdata = nil
	screen.EmitMsg("Stopped")
}

func getDevices(delay int) ([]devType, error) {
	deviceList, err := devices.LoadAllDevices(delay)
	if err != nil {
		return nil, fmt.Errorf("getDevices error: %w", err)
	}

	var guiDeviceList []devType
	for _, dev := range deviceList {
		guiDeviceList = append(guiDeviceList, devType{
			name:       dev.Name,
			addr:       dev.Addr,
			deviceType: dev.Type,
		})
	}

	return guiDeviceList, nil
}

func volumeAction(screen *FyneScreen, up bool) {
	// Handle Chromecast volume
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		if screen.chromecastClient == nil || !screen.chromecastClient.IsConnected() {
			check(screen, errors.New(lang.L("chromecast not connected")))
			return
		}

		// Get current volume from status
		status, err := screen.chromecastClient.GetStatus()
		if err != nil {
			check(screen, errors.New(lang.L("could not get the volume levels")))
			return
		}

		// Volume is 0.0 to 1.0, step by 0.05 (5%)
		newVolume := status.Volume - 0.05
		if up {
			newVolume = status.Volume + 0.05
		}

		// Clamp to valid range
		if newVolume < 0 {
			newVolume = 0
		}
		if newVolume > 1 {
			newVolume = 1
		}

		if err := screen.chromecastClient.SetVolume(newVolume); err != nil {
			check(screen, errors.New(lang.L("could not send volume action")))
		}
		return
	}

	// Handle DLNA volume
	if screen.renderingControlURL == "" {
		check(screen, errors.New(lang.L("please select a device")))
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
		check(screen, errors.New(lang.L("could not get the volume levels")))
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
		check(screen, errors.New(lang.L("could not send volume action")))
	}
}

func queueNext(screen *FyneScreen, clear bool) (*soapcalls.TVPayload, error) {
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

	var mediaFile any = fpath
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
