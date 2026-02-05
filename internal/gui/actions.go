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
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	xfilepicker "github.com/alexballas/xfilepicker/dialog"
	"github.com/pkg/errors"
	"github.com/skratchdot/open-golang/open"
	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/rtmp"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

const filePickerFillSize = 10000

func muteAction(screen *FyneScreen) {
	// Handle icon toggle (mute -> unmute)
	if screen.MuteUnmute.Icon == theme.VolumeMuteIcon() {
		unmuteAction(screen)
		return
	}

	// Handle Chromecast mute
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		go func() {
			if screen.chromecastClient == nil || !screen.chromecastClient.IsConnected() {
				check(screen, errors.New(lang.L("chromecast not connected")))
				return
			}
			if err := screen.chromecastClient.SetMuted(true); err != nil {
				check(screen, errors.New(lang.L("could not send mute action")))
				return
			}
			setMuteUnmuteView("Unmute", screen)
		}()
		return
	}

	// Handle DLNA mute
	if screen.renderingControlURL == "" {
		check(screen, errors.New(lang.L("please select a device")))
		return
	}

	go func() {
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
	}()
}

func unmuteAction(screen *FyneScreen) {
	// Handle Chromecast unmute
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		go func() {
			if screen.chromecastClient == nil || !screen.chromecastClient.IsConnected() {
				check(screen, errors.New(lang.L("chromecast not connected")))
				return
			}
			if err := screen.chromecastClient.SetMuted(false); err != nil {
				check(screen, errors.New(lang.L("could not send mute action")))
				return
			}
			setMuteUnmuteView("Mute", screen)
		}()
		return
	}

	// Handle DLNA unmute
	if screen.renderingControlURL == "" {
		check(screen, errors.New(lang.L("please select a device")))
		return
	}

	go func() {
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
	}()
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

	// Auto-enable transcoding for incompatible Chromecast media
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		screen.checkChromecastCompatibility()
	}
	setPlayPauseView("", screen)
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
	xfilepicker.SetFFmpegPath(screen.ffmpegPath)
	var resumeHotkeys func()
	fd := xfilepicker.NewFileOpen(func(readers []fyne.URIReadCloser, err error) {
		if resumeHotkeys != nil {
			defer resumeHotkeys()
		}
		check(screen, err)

		if readers == nil {
			return
		}
		defer func() {
			for _, i := range readers {
				i.Close()
			}
		}()
		selectMediaFile(screen, readers[0].URI())
	}, w, false)

	if f, ok := fd.(xfilepicker.FilePicker); ok {
		f.SetFilter(storage.NewExtensionFileFilter(screen.mediaFormats))
	}

	if screen.currentmfolder != "" {
		mfileURI := storage.NewFileURI(screen.currentmfolder)
		mfileLister, err := storage.ListerForURI(mfileURI)
		check(screen, err)

		if f, ok := fd.(xfilepicker.FilePicker); ok {
			f.SetLocation(mfileLister)
		}
	}

	resumeHotkeys = suspendHotkeys(screen)
	fd.Show()
	fd.Resize(fyne.NewSize(filePickerFillSize, filePickerFillSize))
}

func subsAction(screen *FyneScreen) {
	w := screen.Current
	var resumeHotkeys func()
	fd := xfilepicker.NewFileOpen(func(readers []fyne.URIReadCloser, err error) {
		if resumeHotkeys != nil {
			defer resumeHotkeys()
		}
		check(screen, err)

		if readers == nil {
			return
		}

		defer func() {
			for _, i := range readers {
				i.Close()
			}
		}()

		selectSubsFile(screen, readers[0].URI())
	}, w, false)

	if f, ok := fd.(xfilepicker.FilePicker); ok {
		f.SetFilter(storage.NewExtensionFileFilter([]string{".srt"}))
	}

	if screen.currentmfolder != "" {
		mfileURI := storage.NewFileURI(screen.currentmfolder)
		mfileLister, err := storage.ListerForURI(mfileURI)
		check(screen, err)
		if err != nil {
			return
		}

		if f, ok := fd.(xfilepicker.FilePicker); ok {
			f.SetLocation(mfileLister)
		}

	}
	resumeHotkeys = suspendHotkeys(screen)
	fd.Show()
	fd.Resize(fyne.NewSize(filePickerFillSize, filePickerFillSize))
}

func playAction(screen *FyneScreen) {
	var mediaFile any

	fyne.Do(func() {
		screen.PlayPause.Disable()
	})

	// Check if there's an active playback session (DLNA or Chromecast) that should be
	// controlled even when browsing other devices. This takes priority over starting
	// new playback on the currently selected device.
	currentState := screen.getScreenState()
	isActivePlayback := currentState == "Playing" || currentState == "Paused"

	// Active DLNA session: tvdata exists and has control URL
	if screen.tvdata != nil && screen.tvdata.ControlURL != "" && isActivePlayback {
		if currentState == "Paused" {
			err := screen.tvdata.SendtoTV("Play")
			check(screen, err)
			return
		}
		if currentState == "Playing" {
			err := screen.tvdata.SendtoTV("Pause")
			check(screen, err)
			return
		}
	}

	// Active Chromecast session: client connected and playing/paused
	if screen.chromecastClient != nil && screen.chromecastClient.IsConnected() && isActivePlayback {
		if currentState == "Paused" {
			if err := screen.chromecastClient.Play(); err != nil {
				check(screen, err)
				return
			}
			setPlayPauseView("Pause", screen)
			screen.updateScreenState("Playing")
			return
		}
		if currentState == "Playing" {
			if err := screen.chromecastClient.Pause(); err != nil {
				check(screen, err)
				return
			}
			setPlayPauseView("Play", screen)
			screen.updateScreenState("Paused")
			return
		}
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

	// Branch based on device type - MUST be first, before any DLNA-specific logic
	// Chromecast has its own status watcher, doesn't need the DLNA timeout mechanism
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		go chromecastPlayAction(screen)
		return
	}

	// DLNA timeout mechanism - re-enable play button if no response after 3 seconds
	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}

	ctx, cancelEnablePlay := context.WithTimeout(context.Background(), 3*time.Second)
	screen.cancelEnablePlay = cancelEnablePlay

	go func() {
		// RTMP wait mechanism
		if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
			if err := waitForRTMPStream(screen); err != nil {
				check(screen, err)
				startAfreshPlayButton(screen)
				return
			}
		}

		// DLNA pause/resume handling for new playback sessions
		// (active sessions are handled above before device type check)
		if currentState == "Paused" {
			err := screen.tvdata.SendtoTV("Play")
			check(screen, err)
			return
		}

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
			if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
				mediaType = "application/vnd.apple.mpegurl"
				screen.SetMediaType(mediaType)
			} else {
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

				// Set casting media type
				screen.SetMediaType(mediaType)

				if !screen.Transcode {
					isSeek = true
				}
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
			// We use its value for many checks in our code.
			screen.mediafile = screen.MediaText.Text

			if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
				mediaType = "application/vnd.apple.mpegurl"
				screen.SetMediaType(mediaType)
				mediaFile = screen.mediafile
			} else {
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

				// Set casting media type
				screen.SetMediaType(mediaType)

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
		}

		if screen.SelectInternalSubs.Selected != "" {
			for n, opt := range screen.SelectInternalSubs.Options {
				if opt == screen.SelectInternalSubs.Selected {
					fyne.Do(func() {
						screen.PlayPause.Text = lang.L("Extracting Subtitles") + "   "
						screen.PlayPause.Refresh()
					})
					tempSubsPath, err := utils.ExtractSub(screen.ffmpegPath, n, screen.mediafile)
					fyne.Do(func() {
						screen.PlayPause.Text = lang.L("Play") + "   "
						screen.PlayPause.Refresh()
					})
					if err != nil {
						break
					}

					screen.tempFiles = append(screen.tempFiles, tempSubsPath)
					screen.subsfile = tempSubsPath
				}
			}
		}

		if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
			screen.tvdata = &soapcalls.TVPayload{
				ControlURL:                  screen.controlURL,
				EventURL:                    screen.eventlURL,
				RenderingControlURL:         screen.renderingControlURL,
				ConnectionManagerURL:        screen.connectionManagerURL,
				MediaURL:                    "http://" + whereToListen + "/rtmp/playlist.m3u8",
				SubtitlesURL:                "http://" + whereToListen + "/rtmp/subs.srt",
				CallbackURL:                 "http://" + whereToListen + "/" + callbackPath,
				MediaType:                   mediaType,
				MediaPath:                   screen.mediafile,
				CurrentTimers:               make(map[string]*time.Timer),
				MediaRenderersStates:        make(map[string]*soapcalls.States),
				InitialMediaRenderersStates: make(map[string]bool),
				Transcode:                   false,
				Seekable:                    false,
				LogOutput:                   screen.Debug,
				FFmpegPath:                  screen.ffmpegPath,
			}
		} else {
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
		}

		if screen.httpserver != nil {
			screen.httpserver.StopServer()
		}

		screen.httpserver = httphandlers.NewServer(whereToListen)
		if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
			screen.httpserver.AddDirectoryHandler("/rtmp/", screen.rtmpHLSURL)
		}

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

	}()

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

	// RTMP wait mechanism
	if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
		if err := waitForRTMPStream(screen); err != nil {
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}
	}

	// Reset seek position for fresh playback (auto-play next file needs this)
	screen.ffmpegSeek = 0

	transcode := screen.Transcode
	ffmpegSeek := screen.ffmpegSeek

	// Handle internal (embedded) subtitles extraction
	if screen.SelectInternalSubs.Selected != "" {
		for n, opt := range screen.SelectInternalSubs.Options {
			if opt == screen.SelectInternalSubs.Selected {
				fyne.Do(func() {
					screen.PlayPause.Text = lang.L("Extracting Subtitles") + "   "
					screen.PlayPause.Refresh()
				})
				tempSubsPath, err := utils.ExtractSub(screen.ffmpegPath, n, screen.mediafile)
				fyne.Do(func() {
					screen.PlayPause.Text = lang.L("Play") + "   "
					screen.PlayPause.Refresh()
				})
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

		// Enable debug logging (same pattern as TVPayload)
		client.LogOutput = screen.Debug

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

		if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
			mediaType = "application/vnd.apple.mpegurl"
			screen.SetMediaType(mediaType)
			transcode = true
		} else {
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

			// Chromecast handles images and audio natively - never transcode these
			mediaTypeSlice := strings.Split(mediaType, "/")
			if len(mediaTypeSlice) > 0 && (mediaTypeSlice[0] == "image" || mediaTypeSlice[0] == "audio") {
				transcode = false
			}

			// Set casting media type
			screen.SetMediaType(mediaType)

			if screen.selectedDevice.isAudioOnly && (strings.Contains(mediaType, "video") || strings.Contains(mediaType, "image")) {
				check(screen, errors.New(lang.L("Video/Image file not supported by audio-only device")))
				startAfreshPlayButton(screen)
				return
			}
		}

		if transcode {
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

			var stream io.ReadCloser
			if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
				// No need to stream URL for RTMP, we serve HLS from temp dir
			} else {
				stream, err = utils.StreamURL(context.Background(), mediaURL)
				if err != nil {
					check(screen, err)
					startAfreshPlayButton(screen)
					return
				}
			}

			subsPath := ""
			if screen.subsfile != "" {
				subsPath = screen.subsfile
			}

			tcOpts := &utils.TranscodeOptions{
				FFmpegPath:   screen.ffmpegPath,
				SubsPath:     subsPath,
				SeekSeconds:  0,
				SubtitleSize: utils.SubtitleSizeMedium,
				LogOutput:    screen.Debug,
			}

			screen.mediaDuration = 0
			mediaFilename := "/" + utils.ConvertFilename(mediaURL)
			if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
				screen.httpserver.AddHLSHandler("/live/", screen.rtmpServer.TempDir())
				mediaURL = "http://" + whereToListen + "/live/playlist.m3u8"
				mediaType = "application/vnd.apple.mpegurl"
			} else {
				screen.httpserver.AddHandler(mediaFilename, nil, tcOpts, stream)
				mediaURL = "http://" + whereToListen + mediaFilename
			}

			go func() {
				screen.httpserver.StartServing(serverStarted)
				serverCTXStop()
			}()

			if err := <-serverStarted; err != nil {
				check(screen, err)
				startAfreshPlayButton(screen)
				return
			}

			// mediaURL is already set correctly above
			if screen.rtmpServerCheck == nil || !screen.rtmpServerCheck.Checked {
				mediaType = "video/mp4"
			}
		} else {
			var cancel context.CancelFunc
			serverStoppedCTX, cancel = context.WithCancel(context.Background())
			screen.serverStopCTX = serverStoppedCTX
			go func() { <-serverStoppedCTX.Done(); cancel() }()
		}

	} else if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
		// RTMP Mode: No need for local file checks
		mediaType = "application/vnd.apple.mpegurl"
		screen.SetMediaType(mediaType)
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

		// Chromecast handles images and audio natively - never transcode these
		mediaTypeSlice := strings.Split(mediaType, "/")
		if len(mediaTypeSlice) > 0 && (mediaTypeSlice[0] == "image" || mediaTypeSlice[0] == "audio") {
			transcode = false
		}

		// Set casting media type
		screen.SetMediaType(mediaType)

		if screen.selectedDevice.isAudioOnly && (strings.Contains(mediaType, "video") || strings.Contains(mediaType, "image")) {
			check(screen, errors.New(lang.L("Video/Image file not supported by audio-only device")))
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

		// Create TranscodeOptions if transcoding enabled
		var tcOpts *utils.TranscodeOptions
		if transcode {
			// Get actual media duration from ffprobe (Chromecast can't report it for transcoded streams)
			if duration, err := utils.DurationForMediaSeconds(screen.ffmpegPath, screen.mediafile); err == nil {
				screen.mediaDuration = duration
			}

			// Determine subtitle path for burning (only if user selected)
			subsPath := ""
			if screen.subsfile != "" {
				subsPath = screen.subsfile
			}

			tcOpts = &utils.TranscodeOptions{
				FFmpegPath:   screen.ffmpegPath,
				SubsPath:     subsPath,
				SeekSeconds:  ffmpegSeek,
				SubtitleSize: utils.SubtitleSizeMedium,
				LogOutput:    screen.Debug,
			}
			// Update content type for transcoded output
			mediaType = "video/mp4"
		} else {
			// Clear stored duration for non-transcoded streams (Chromecast reports it correctly)
			screen.mediaDuration = 0
		}

		go func() {
			screen.httpserver.StartSimpleServerWithTranscode(serverStarted, screen.mediafile, tcOpts)
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
	isRTMP := screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked
	if screen.subsfile != "" && screen.httpserver != nil && (!transcode || isRTMP) {
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
					screen.httpserver.AddHandler("/subtitles.vtt", nil, nil, webvttData)
					subtitleURL = "http://" + mediaURLParsed.Host + "/subtitles.vtt"
				}
			case ".vtt":
				screen.httpserver.AddHandler("/subtitles.vtt", nil, nil, screen.subsfile)
				subtitleURL = "http://" + mediaURLParsed.Host + "/subtitles.vtt"
			}
		}
	}

	// Load media and update UI on success
	go func() {
		// Use LIVE stream type for URL streams (DMR shows LIVE badge, but buffer unchanged)
		live := screen.ExternalMediaURL.Checked
		if err := client.Load(mediaURL, mediaType, ffmpegSeek, screen.mediaDuration, subtitleURL, live); err != nil {
			check(screen, fmt.Errorf("chromecast load: %w", err))
			startAfreshPlayButton(screen)
			return
		}
	}()

	go chromecastStatusWatcher(serverStoppedCTX, screen)
}

// chromecastTranscodedSeek performs a seek on transcoded Chromecast streams
// by restarting the HTTP server with new seek position while keeping the connection open.
// This is much faster than stopAction+playAction which closes/reopens the connection.
// Runs fully async to prevent UI freeze during buffering.
func chromecastTranscodedSeek(screen *FyneScreen, seekPos int) {
	// Capture client reference before async operation
	client := screen.chromecastClient
	if client == nil || !client.IsConnected() {
		return
	}
	// Update seek position immediately (used by status watcher)
	screen.ffmpegSeek = seekPos
	// Run entire seek operation in background to prevent UI freeze
	go func() {
		// Stop HTTP server (kills FFmpeg) but keep Chromecast client connected
		if screen.httpserver != nil {
			screen.httpserver.StopServer()
		}
		// Transcoded streams always output video/mp4
		mediaType := "video/mp4"
		whereToListen, err := utils.URLtoListenIPandPort(screen.selectedDevice.addr)
		if err != nil {
			check(screen, err)
			return
		}
		// Create new HTTP server with new seek position
		screen.httpserver = httphandlers.NewServer(whereToListen)
		serverStarted := make(chan error)
		serverStoppedCTX, serverCTXStop := context.WithCancel(context.Background())
		screen.serverStopCTX = serverStoppedCTX
		screen.cancelServerStop = serverCTXStop
		// Determine subtitle path for burning
		subsPath := ""
		if screen.subsfile != "" {
			subsPath = screen.subsfile
		}
		tcOpts := &utils.TranscodeOptions{
			FFmpegPath:   screen.ffmpegPath,
			SubsPath:     subsPath,
			SeekSeconds:  seekPos,
			SubtitleSize: utils.SubtitleSizeMedium,
			LogOutput:    screen.Debug,
		}
		go func() {
			screen.httpserver.StartSimpleServerWithTranscode(serverStarted, screen.mediafile, tcOpts)
			serverCTXStop()
		}()

		if err := <-serverStarted; err != nil {
			check(screen, err)
			return
		}
		mediaURL := "http://" + whereToListen + "/" + utils.ConvertFilename(screen.mediafile)
		// Load media on existing connection (skips 2-second receiver launch delay)
		// No subtitles needed since they're burned in during transcoding
		// live=false because this is local file playback (seeking)
		if err := client.LoadOnExisting(mediaURL, mediaType, 0, screen.mediaDuration, "", false); err != nil {
			check(screen, fmt.Errorf("chromecast seek load: %w", err))
			return
		}
		// Restart status watcher
		go chromecastStatusWatcher(serverStoppedCTX, screen)
	}()
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
			// Capture client once to avoid race with stopAction nilling it
			client := screen.chromecastClient
			if client == nil || !client.IsConnected() {
				return
			}

			status, err := client.GetStatus()
			if err != nil {
				continue
			}
			// Update state based on player state
			switch status.PlayerState {
			case "BUFFERING":
				// Media is loading - mark as started but don't update slider
				// (Chromecast reports 0 duration/time during buffering which would zero out slider)
				mediaStarted = true
			case "PLAYING":
				mediaStarted = true
				if screen.getScreenState() != "Playing" {
					// Double check to avoid a race condition when clicking the stop button
					if client.IsConnected() {
						setPlayPauseView("Pause", screen)
						screen.updateScreenState("Playing")
					}
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

			// Update slider position (only if media has started and not buffering)
			// Skip BUFFERING state - Chromecast reports 0 duration/time during buffering
			if status.PlayerState == "BUFFERING" {
				continue
			}

			// For transcoded streams, use stored duration from ffprobe (Chromecast only knows buffered duration)
			duration := float64(status.Duration)
			currentTime := float64(status.CurrentTime)
			// If we have a stored duration (from ffprobe for transcoded streams), always use it
			// This is more reliable than checking screen.Transcode which might get out of sync
			if screen.mediaDuration > 0 {
				duration = screen.mediaDuration
				// Add seek offset to show correct position in original file
				currentTime = float64(status.CurrentTime) + float64(screen.ffmpegSeek)
			}

			if mediaStarted && !screen.sliderActive && duration > 0 {
				progress := (currentTime / duration) * screen.SlideBar.Max
				fyne.Do(func() {
					screen.SlideBar.SetValue(progress)
				})

				// Update time labels
				current, _ := utils.SecondsToClockTime(int(currentTime))
				total, _ := utils.SecondsToClockTime(int(duration))
				screen.CurrentPos.Set(current)
				screen.EndPos.Set(total)

				// Fallback: Detect media completion when CurrentTime reaches Duration
				// go-chromecast doesn't always report IDLE when media finishes
				// Using 1.5 second threshold since Chromecast stops updating ~1-2s early
				if currentTime >= duration-1.5 && duration > 0 {
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

	// Reset slider and times (needed for Chromecast which doesn't use sliderUpdate loop)
	fyne.Do(func() {
		if !screen.ExternalMediaURL.Checked {
			screen.SkipNextButton.Enable()
		}
		screen.SlideBar.SetValue(0)
	})
	screen.CurrentPos.Set("00:00:00")
	screen.EndPos.Set("00:00:00")
	screen.ffmpegSeek = 0
	screen.mediaDuration = 0
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

					mediaName, mediaPath := getNextMedia(screen)
					screen.mediafile = mediaPath
					fyne.Do(func() {
						screen.MediaText.Text = mediaName
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

func clearmediaAction(screen *FyneScreen) {
	screen.MediaText.SetText("")
	screen.mediafile = ""
	screen.SelectInternalSubs.Options = []string{}
	screen.SelectInternalSubs.PlaceHolder = lang.L("No Embedded Subs")
	screen.SelectInternalSubs.ClearSelected()
	screen.SelectInternalSubs.Disable()
	setPlayPauseView("", screen)
}

func clearsubsAction(screen *FyneScreen) {
	screen.SelectInternalSubs.ClearSelected()
	screen.SubsText.SetText("")
	screen.subsfile = ""
}

func skipNextAction(screen *FyneScreen) {
	if screen.mediafile == "" {
		check(screen, errors.New(lang.L("please select a media file")))
		return
	}

	// Check if any device is selected (DLNA uses controlURL, Chromecast uses selectedDevice)
	if screen.controlURL == "" && screen.selectedDeviceType != devices.DeviceTypeChromecast {
		check(screen, errors.New(lang.L("please select a device")))
		return
	}

	fyne.Do(func() {
		screen.PlayPause.Disable()
		screen.SkipNextButton.Disable()
	})

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

		go func() {
			// Determine if transcoding is enabled
			transcode := screen.Transcode

			// Chromecast handles images and audio natively - never transcode these
			mediaTypeSlice := strings.Split(mediaType, "/")
			if len(mediaTypeSlice) > 0 && (mediaTypeSlice[0] == "image" || mediaTypeSlice[0] == "audio") {
				transcode = false
			}

			// Set casting media type
			screen.SetMediaType(mediaType)

			// Get server address
			whereToListen := screen.httpserver.GetAddr()

			var mediaURL string
			var subtitleURL string
			var serverStoppedCTX context.Context

			if transcode {
				// TRANSCODING PATH: Stop server and restart with new file and transcode options
				if screen.httpserver != nil {
					screen.httpserver.StopServer()
				}

				// Get actual media duration from ffprobe (Chromecast can't report it for transcoded streams)
				if duration, err := utils.DurationForMediaSeconds(screen.ffmpegPath, screen.mediafile); err == nil {
					screen.mediaDuration = duration
				}

				// Reset seek position for new file
				screen.ffmpegSeek = 0

				// Determine subtitle path for burning (only if user selected)
				subsPath := ""
				if screen.subsfile != "" {
					subsPath = screen.subsfile
				}

				tcOpts := &utils.TranscodeOptions{
					FFmpegPath:   screen.ffmpegPath,
					SubsPath:     subsPath,
					SeekSeconds:  0,
					SubtitleSize: utils.SubtitleSizeMedium,
					LogOutput:    screen.Debug,
				}

				// Create new HTTP server with transcoding
				screen.httpserver = httphandlers.NewServer(whereToListen)
				serverStarted := make(chan error)
				var serverCTXStop context.CancelFunc
				serverStoppedCTX, serverCTXStop = context.WithCancel(context.Background())
				screen.serverStopCTX = serverStoppedCTX
				screen.cancelServerStop = serverCTXStop

				go func() {
					screen.httpserver.StartSimpleServerWithTranscode(serverStarted, screen.mediafile, tcOpts)
					serverCTXStop()
				}()

				if err := <-serverStarted; err != nil {
					check(screen, err)
					return
				}

				// Transcoded output is always video/mp4
				mediaType = "video/mp4"
				mediaURL = "http://" + whereToListen + "/" + utils.ConvertFilename(screen.mediafile)
				// Subtitles are burned in during transcoding, no separate URL needed

			} else {
				// NON-TRANSCODING PATH: Just update handlers on existing server
				// Clear stored duration for non-transcoded streams (Chromecast reports it correctly)
				screen.mediaDuration = 0

				// Get subtitle URL if needed (remove old handler first)
				screen.httpserver.RemoveHandler("/subtitles.vtt")
				if screen.subsfile != "" {
					ext := strings.ToLower(filepath.Ext(screen.subsfile))
					switch ext {
					case ".srt":
						webvttData, err := utils.ConvertSRTtoWebVTT(screen.subsfile)
						if err == nil {
							screen.httpserver.AddHandler("/subtitles.vtt", nil, nil, webvttData)
							subtitleURL = "http://" + whereToListen + "/subtitles.vtt"
						}
					case ".vtt":
						screen.httpserver.AddHandler("/subtitles.vtt", nil, nil, screen.subsfile)
						subtitleURL = "http://" + whereToListen + "/subtitles.vtt"
					}
				}

				// Remove old media handler and add new one
				// Handler paths use filepath.Base (decoded) because r.URL.Path is decoded by Go's HTTP server
				// URL uses ConvertFilename (encoded) for valid HTTP URL with special characters
				oldHandlerPath := "/" + filepath.Base(oldMediaPath)
				newHandlerPath := "/" + filepath.Base(screen.mediafile)
				screen.httpserver.RemoveHandler(oldHandlerPath)
				screen.httpserver.AddHandler(newHandlerPath, nil, nil, screen.mediafile)

				// Build media URL using URL-encoded filename (for special chars like brackets)
				mediaURL = "http://" + whereToListen + "/" + utils.ConvertFilename(screen.mediafile)

				// Use existing server context
				serverStoppedCTX = screen.serverStopCTX
			}

			// Set state to Waiting to ensure status watcher triggers UI update when playing starts
			screen.updateScreenState("Waiting")

			// Load new media on existing connection (async to avoid blocking)
			// live=false for skip-next (local files don't need LIVE stream type)
			go func() {
				if err := screen.chromecastClient.Load(mediaURL, mediaType, 0, screen.mediaDuration, subtitleURL, false); err != nil {
					check(screen, fmt.Errorf("chromecast load: %w", err))
					startAfreshPlayButton(screen)
					return
				}
			}()

			// Restart status watcher if transcoding (server was restarted)
			if transcode && serverStoppedCTX != nil {
				go chromecastStatusWatcher(serverStoppedCTX, screen)
			}

		}()
		return
	}

	// For DLNA or if Chromecast client not ready: use stop+play
	// We need to stop synchronously to avoid race conditions with PlayAction
	// which might be cancelled by the async StopAction or conflict with it.

	fyne.Do(func() {
		screen.PlayPause.SetText(lang.L("Play"))
		screen.PlayPause.SetIcon(theme.MediaPlayIcon())
		screen.PlayPause.Refresh()
	})

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
	defer mfile.Close()

	mediaType, err := utils.GetMimeDetailsFromFile(mfile)
	check(screen, err)
	if err != nil {
		return
	}

	mediaTypeSlice := strings.Split(mediaType, "/")
	switch mediaTypeSlice[0] {
	case "image":
		fyne.Do(func() {
			img := canvas.NewImageFromFile(screen.mediafile)
			img.FillMode = canvas.ImageFillContain
			img.ScaleMode = canvas.ImageScaleFastest
			imgw := fyne.CurrentApp().NewWindow(filepath.Base(screen.mediafile))
			imgw.SetContent(img)
			imgw.Resize(fyne.NewSize(800, 600))
			imgw.CenterOnScreen()
			imgw.Show()
		})
	default:
		go func() {
			err := open.Run(screen.mediafile)
			check(screen, err)
		}()
	}
}

func stopAction(screen *FyneScreen) {
	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")

	// Clear casting media type immediately
	screen.SetMediaType("")

	if screen.chromecastClient != nil && screen.chromecastClient.IsConnected() {
		// Capture references before clearing
		client := screen.chromecastClient
		server := screen.httpserver

		// Clear references immediately to prevent status watcher from continuing
		screen.chromecastClient = nil
		screen.httpserver = nil

		// Reset progress bar and time labels immediately (UI update)
		fyne.Do(func() {
			screen.SlideBar.SetValue(0)
			screen.CurrentPos.Set("00:00:00")
			screen.EndPos.Set("00:00:00")
		})
		// Reset transcoding seek state
		screen.ffmpegSeek = 0
		screen.mediaDuration = 0

		// Run blocking network operations in background
		go func() {
			_ = client.Stop()
			client.Close(false)
			if server != nil {
				server.StopServer()
			}
		}()
		return
	}

	if screen.tvdata == nil || screen.tvdata.ControlURL == "" {
		return
	}

	// Capture references before clearing
	tvdata := screen.tvdata
	server := screen.httpserver

	// Clear references immediately
	screen.tvdata = nil
	screen.httpserver = nil

	// Run blocking network operations in background
	go func() {
		if tvdata != nil && tvdata.ControlURL != "" {
			_ = tvdata.SendtoTV("Stop")
		}
		if server != nil {
			server.StopServer()
		}
	}()

}

func getDevices(delay int) ([]devType, error) {
	deviceList, err := devices.LoadAllDevices(delay)
	if err != nil {
		return nil, fmt.Errorf("getDevices error: %w", err)
	}

	var guiDeviceList []devType
	for _, dev := range deviceList {
		guiDeviceList = append(guiDeviceList, devType{
			name:        dev.Name,
			addr:        dev.Addr,
			deviceType:  dev.Type,
			isAudioOnly: dev.IsAudioOnly,
		})
	}

	return guiDeviceList, nil
}

func volumeAction(screen *FyneScreen, up bool) {
	go func() {
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
	}()
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

	screen.httpserver.AddHandler(mURL.Path, nextTvData, nil, mediaFile)
	screen.httpserver.AddHandler(sURL.Path, nil, nil, spath)

	if err := nextTvData.SendtoTV("Queue"); err != nil {
		return nil, err
	}

	return nextTvData, nil
}

func startRTMPServer(screen *FyneScreen) {
	screen.rtmpMu.Lock()
	defer screen.rtmpMu.Unlock()

	if screen.rtmpServer != nil {
		return
	}

	screen.rtmpServerCheck.Disable()

	go func() {
		screen.rtmpMu.Lock()
		screen.rtmpServer = rtmp.NewServer()
		streamKey := fyne.CurrentApp().Preferences().String("RTMPStreamKey")
		port := fyne.CurrentApp().Preferences().StringWithFallback("RTMPPort", "1935")

		// Async start
		hlsDir, err := screen.rtmpServer.Start(screen.ffmpegPath, streamKey, port)
		if err != nil {
			check(screen, fmt.Errorf("RTMP server error: %w", err))
			// Restore UI on failure
			screen.rtmpServer = nil
			screen.rtmpMu.Unlock()
			fyne.Do(func() {
				screen.rtmpServerCheck.Enable()
				screen.rtmpServerCheck.SetChecked(false)
			})
			return
		}

		// Monitor process health in background
		go func() {
			err := screen.rtmpServer.Wait()
			// Only react if we didn't intentionally stop it
			if screen.rtmpServer != nil {
				errMsg := lang.L("RTMP server stopped unexpectedly")
				if err != nil {
					errMsg = fmt.Sprintf("%s: %v", errMsg, err)
				}
				check(screen, errors.New(errMsg))
				stopRTMPServer(screen)
				stopAction(screen)
			}
		}()

		// Successful start - Update UI
		fyne.Do(func() {
			screen.rtmpServerCheck.Enable()
			screen.rtmpPrevExternalMediaURL = screen.ExternalMediaURL.Checked
			screen.rtmpPrevMediaText = screen.MediaText.Text
			screen.rtmpPrevMediaFile = screen.mediafile

			// Disable other media inputs
			screen.ExternalMediaURL.SetChecked(true)
			screen.ExternalMediaURL.Disable()
			screen.MediaBrowse.Disable()
			screen.MediaText.Disable()
			screen.ClearMedia.Disable()
			screen.TranscodeCheckBox.SetChecked(false)
			screen.TranscodeCheckBox.Disable()
			screen.SlideBar.Disable()

			// Show RTMP URL
			ip := utils.GetOutboundIP()
			if ip == "" {
				ip = "127.0.0.1"
			}
			screen.rtmpURLEntry.SetText(fmt.Sprintf("rtmp://%s:%s/live/", ip, port))
			screen.rtmpKeyEntry.SetText(streamKey)
			screen.rtmpURLCard.Show()

			screen.rtmpHLSURL = hlsDir
			// Set text to indicate streaming mode, but keep disabled
			screen.MediaText.SetText(lang.L("RTMP Live Stream"))
			screen.mediafile = lang.L("RTMP Live Stream")
			setPlayPauseView("", screen)
		})
		screen.rtmpMu.Unlock()
	}()
}

func stopRTMPServer(screen *FyneScreen) {
	screen.rtmpMu.Lock()
	defer screen.rtmpMu.Unlock()

	if screen.rtmpServer == nil {
		fyne.Do(func() {
			resetRTMPUI(screen)
		})
		return
	}

	screen.rtmpServerCheck.Disable()

	go func() {
		screen.rtmpMu.Lock()
		srv := screen.rtmpServer
		screen.rtmpServer = nil // Mark as stopped/stopping
		if srv != nil {
			srv.Stop()
		}

		// Remove HLS handler if any
		if screen.httpserver != nil {
			screen.httpserver.RemoveDirectoryHandler("/rtmp/")
		}

		fyne.Do(func() {
			resetRTMPUI(screen)
			screen.rtmpServerCheck.Enable()
		})
		screen.rtmpMu.Unlock()
	}()
}

func resetRTMPUI(screen *FyneScreen) {
	screen.rtmpServerCheck.SetChecked(false)
	screen.ExternalMediaURL.SetChecked(screen.rtmpPrevExternalMediaURL)
	screen.ExternalMediaURL.Enable()

	if screen.ExternalMediaURL.Checked {
		screen.MediaBrowse.Disable()
		screen.MediaText.Enable()
	} else {
		screen.MediaBrowse.Enable()
		screen.MediaText.Disable()
	}

	screen.ClearMedia.Enable()
	if err := utils.CheckFFmpeg(screen.ffmpegPath); err == nil {
		screen.TranscodeCheckBox.Enable()
	}
	screen.SlideBar.Enable()
	screen.rtmpURLCard.Hide()
	screen.rtmpURLEntry.SetText("")
	screen.rtmpKeyEntry.SetText("")

	screen.rtmpURLCard.Hide()
	screen.rtmpURLEntry.SetText("")
	screen.rtmpKeyEntry.SetText("")

	screen.MediaText.SetText(screen.rtmpPrevMediaText)
	screen.mediafile = screen.rtmpPrevMediaFile

	setPlayPauseView("", screen)
}

func waitForRTMPStream(screen *FyneScreen) error {
	screen.rtmpMu.Lock()
	if screen.rtmpServer == nil {
		screen.rtmpMu.Unlock()
		return errors.New(lang.L("RTMP server not started"))
	}
	playlistPath := filepath.Join(screen.rtmpServer.TempDir(), "playlist.m3u8")
	screen.rtmpMu.Unlock()

	fyne.Do(func() {
		screen.PlayPause.SetText(lang.L("Waiting for Stream..."))
		screen.PlayPause.Disable()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return errors.New(lang.L("RTMP stream not found. Please start streaming from OBS first."))
		case <-ticker.C:
			if _, err := os.Stat(playlistPath); err == nil {
				return nil
			}
		}
	}
}
