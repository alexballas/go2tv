//go:build android || ios

package gui

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"github.com/pkg/errors"
	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

func muteAction(screen *FyneScreen) {
	w := screen.Current

	// Handle icon toggle (mute -> unmute)
	if screen.MuteUnmute.Icon == theme.VolumeMuteIcon() {
		unmuteAction(screen)
		return
	}

	// Handle Chromecast mute
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		go func() {
			if screen.chromecastClient == nil || !screen.chromecastClient.IsConnected() {
				check(w, errors.New(lang.L("chromecast not connected")))
				return
			}
			if err := screen.chromecastClient.SetMuted(true); err != nil {
				check(w, errors.New(lang.L("could not send mute action")))
				return
			}
			setMuteUnmuteView("Unmute", screen)
		}()
		return
	}

	// Handle DLNA mute
	if screen.renderingControlURL == "" {
		check(w, errors.New(lang.L("please select a device")))
		return
	}

	go func() {
		if screen.tvdata == nil {
			screen.tvdata = &soapcalls.TVPayload{RenderingControlURL: screen.renderingControlURL}
		}

		if err := screen.tvdata.SetMuteSoapCall("1"); err != nil {
			check(w, errors.New(lang.L("could not send mute action")))
			return
		}

		setMuteUnmuteView("Unmute", screen)
	}()
}

func unmuteAction(screen *FyneScreen) {
	w := screen.Current

	// Handle Chromecast unmute
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		go func() {
			if screen.chromecastClient == nil || !screen.chromecastClient.IsConnected() {
				check(w, errors.New(lang.L("chromecast not connected")))
				return
			}
			if err := screen.chromecastClient.SetMuted(false); err != nil {
				check(w, errors.New(lang.L("could not send mute action")))
				return
			}
			setMuteUnmuteView("Mute", screen)
		}()
		return
	}

	// Handle DLNA unmute
	if screen.renderingControlURL == "" {
		check(w, errors.New(lang.L("please select a device")))
		return
	}

	go func() {
		if screen.tvdata == nil {
			screen.tvdata = &soapcalls.TVPayload{RenderingControlURL: screen.renderingControlURL}
		}

		if err := screen.tvdata.SetMuteSoapCall("0"); err != nil {
			check(w, errors.New(lang.L("could not send mute action")))
			return
		}

		setMuteUnmuteView("Mute", screen)
	}()
}

func mediaAction(screen *FyneScreen) {
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

func subsAction(screen *FyneScreen) {
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

func playAction(screen *FyneScreen) {
	var mediaFile, subsFile any
	w := screen.Current

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
			check(w, err)
			return
		}
		if screen.PlayPause.Text == "Pause" {
			err := screen.tvdata.SendtoTV("Pause")
			check(w, err)
			return
		}
	}

	// Active Chromecast session: client connected and playing/paused
	if screen.chromecastClient != nil && screen.chromecastClient.IsConnected() && isActivePlayback {
		if currentState == "Paused" {
			if err := screen.chromecastClient.Play(); err != nil {
				check(w, err)
				return
			}
			setPlayPauseView("Pause", screen)
			screen.updateScreenState("Playing")
			return
		}
		if screen.PlayPause.Text == "Pause" {
			if err := screen.chromecastClient.Pause(); err != nil {
				check(w, err)
				return
			}
			setPlayPauseView("Play", screen)
			screen.updateScreenState("Paused")
			return
		}
	}

	// Branch based on device type - MUST be first, before any DLNA-specific logic
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		go chromecastPlayAction(screen)
		return
	}

	// DLNA timeout mechanism - re-enable play button if no response after 5 seconds
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

	// DLNA pause/resume handling for new playback sessions
	// (active sessions are handled above before device type check)
	if currentState == "Paused" {
		err := screen.tvdata.SendtoTV("Play")
		check(w, err)
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
		// On Android/iOS, Fyne storage only provides io.ReadCloser which doesn't support seeking.
		// http.ServeContent requires io.ReadSeeker for range requests (video seeking).
		// Solution: Copy file content to a temp file that we can use with os.File.
		mediaURLinfo, err := storage.Reader(screen.mediafile)
		check(screen.Current, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		mediaType, err = utils.GetMimeDetailsFromStream(mediaURLinfo)
		mediaURLinfo.Close()
		check(w, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		// Set casting media type
		screen.SetMediaType(mediaType)

		// Images: read to byte buffer (small, no seeking needed)
		if strings.Contains(mediaType, "image") {
			mediaReader, err := storage.Reader(screen.mediafile)
			if err != nil {
				check(w, err)
				startAfreshPlayButton(screen)
				return
			}
			readerToBytes, err := io.ReadAll(mediaReader)
			mediaReader.Close()
			if err != nil {
				startAfreshPlayButton(screen)
				return
			}
			mediaFile = readerToBytes
		} else {
			// Video/Audio: copy to temp file for seeking support
			mediaReader, err := storage.Reader(screen.mediafile)
			if err != nil {
				check(w, err)
				startAfreshPlayButton(screen)
				return
			}

			ext := filepath.Ext(screen.MediaText.Text)
			tempFile, err := os.CreateTemp("", "go2tv-*"+ext)
			if err != nil {
				mediaReader.Close()
				check(w, fmt.Errorf("temp file create: %w", err))
				startAfreshPlayButton(screen)
				return
			}

			if _, err := io.Copy(tempFile, mediaReader); err != nil {
				mediaReader.Close()
				tempFile.Close()
				os.Remove(tempFile.Name())
				check(w, fmt.Errorf("temp file copy: %w", err))
				startAfreshPlayButton(screen)
				return
			}
			mediaReader.Close()
			tempFile.Close()

			// Store for cleanup in stopAction and use path for serving
			screen.tempMediaFile = tempFile.Name()
			mediaFile = screen.tempMediaFile
		}
	}

	if screen.subsfile != nil {
		subsFile, err = storage.Reader(screen.subsfile)
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

func clearmediaAction(screen *FyneScreen) {
	screen.MediaText.Text = ""
	screen.mediafile = nil
	screen.MediaText.Refresh()
}

func clearsubsAction(screen *FyneScreen) {
	screen.SubsText.Text = ""
	screen.subsfile = nil
	screen.SubsText.Refresh()
}

func stopAction(screen *FyneScreen) {
	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")

	// Clear casting media type immediately
	screen.SetMediaType("")

	// Clean up temp media file
	if screen.tempMediaFile != "" {
		os.Remove(screen.tempMediaFile)
		screen.tempMediaFile = ""
	}

	// Handle Chromecast stop
	if screen.chromecastClient != nil && screen.chromecastClient.IsConnected() {
		_ = screen.chromecastClient.Stop()
		screen.chromecastClient.Close(false)
		screen.chromecastClient = nil
		if screen.httpserver != nil {
			screen.httpserver.StopServer()
		}
		return
	}

	// Handle DLNA stop
	if screen.tvdata == nil || screen.tvdata.ControlURL == "" {
		return
	}

	// Run network stop in background
	go func() {
		// Capture references for safety within goroutine
		tvdata := screen.tvdata
		if tvdata != nil && tvdata.ControlURL != "" {
			_ = tvdata.SendtoTV("Stop")
		}

		if server := screen.httpserver; server != nil {
			server.StopServer()
		}

		screen.tvdata = nil
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
	w := screen.Current
	go func() {

		// Handle Chromecast volume
		if screen.selectedDeviceType == devices.DeviceTypeChromecast {
			if screen.chromecastClient == nil || !screen.chromecastClient.IsConnected() {
				check(w, errors.New(lang.L("chromecast not connected")))
				return
			}

			status, err := screen.chromecastClient.GetStatus()
			if err != nil {
				check(w, errors.New(lang.L("could not get the volume levels")))
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
				check(w, errors.New(lang.L("could not send volume action")))
			}
			return
		}

		// Handle DLNA volume
		if screen.renderingControlURL == "" {
			check(w, errors.New(lang.L("please select a device")))
			return
		}

		if screen.tvdata == nil {
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
	}()
}

func startAfreshPlayButton(screen *FyneScreen) {
	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}

	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")
}

// chromecastPlayAction handles playback on Chromecast devices.
// Supports both local files (via internal HTTP server) and external URLs (direct).
func chromecastPlayAction(screen *FyneScreen) {
	w := screen.Current

	// Handle pause/resume if already playing - query Chromecast status directly
	if screen.chromecastClient != nil && screen.chromecastClient.IsConnected() {
		status, err := screen.chromecastClient.GetStatus()
		if err == nil {
			switch status.PlayerState {
			case "PLAYING":
				if err := screen.chromecastClient.Pause(); err != nil {
					check(w, err)
					return
				}
				setPlayPauseView("Play", screen)
				screen.updateScreenState("Paused")
				return
			case "PAUSED":
				if err := screen.chromecastClient.Play(); err != nil {
					check(w, err)
					startAfreshPlayButton(screen)
					return
				}
				setPlayPauseView("Pause", screen)
				screen.updateScreenState("Playing")
				return
			}
		}
	}

	// Validate media file or URL
	if screen.mediafile == nil && screen.MediaText.Text == "" {
		check(w, errors.New(lang.L("please select a media file or enter a media URL")))
		startAfreshPlayButton(screen)
		return
	}

	// Reuse existing client if connected, otherwise create new one
	client := screen.chromecastClient
	if client == nil || !client.IsConnected() {
		var err error
		client, err = castprotocol.NewCastClient(screen.selectedDevice.addr)
		if err != nil {
			check(w, fmt.Errorf("chromecast init: %w", err))
			startAfreshPlayButton(screen)
			return
		}

		// Note: Debug logging disabled on mobile - zerolog crashes on Android
		// client.LogOutput = screen.Debug

		if err := client.Connect(); err != nil {
			check(w, fmt.Errorf("chromecast connect: %w", err))
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

		mediaURLinfo, err := utils.StreamURL(context.Background(), mediaURL)
		if err != nil {
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}
		mediaType, err = utils.GetMimeDetailsFromStream(mediaURLinfo)
		mediaURLinfo.Close()
		if err != nil {
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}

		if screen.selectedDevice.isAudioOnly && (strings.Contains(mediaType, "video") || strings.Contains(mediaType, "image")) {
			check(w, errors.New(lang.L("Video/Image file not supported by audio-only device")))
			startAfreshPlayButton(screen)
			return
		}

		var cancel context.CancelFunc
		serverStoppedCTX, cancel = context.WithCancel(context.Background())
		screen.serverStopCTX = serverStoppedCTX
		go func() { <-serverStoppedCTX.Done(); cancel() }()

	} else {
		// LOCAL FILE: Serve via internal HTTP server
		// On Android/iOS, Fyne storage only provides io.ReadCloser which doesn't support seeking.
		// http.ServeContent requires io.ReadSeeker for range requests (video seeking).
		// Solution: Copy file content to a temp file that we can use with os.File.
		mediaReader, err := storage.Reader(screen.mediafile)
		if err != nil {
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}

		mediaType, err = utils.GetMimeDetailsFromStream(mediaReader)
		mediaReader.Close()
		if err != nil {
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}

		if screen.selectedDevice.isAudioOnly && (strings.Contains(mediaType, "video") || strings.Contains(mediaType, "image")) {
			check(w, errors.New(lang.L("Video/Image file not supported by audio-only device")))
			startAfreshPlayButton(screen)
			return
		}

		whereToListen, err := utils.URLtoListenIPandPort(screen.selectedDevice.addr)
		if err != nil {
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}

		if screen.httpserver != nil {
			screen.httpserver.StopServer()
		}

		screen.httpserver = httphandlers.NewServer(whereToListen)
		var serverCTXStop context.CancelFunc
		serverStoppedCTX, serverCTXStop = context.WithCancel(context.Background())
		screen.serverStopCTX = serverStoppedCTX
		screen.cancelServerStop = serverCTXStop

		// Get media reader for copying to temp file
		mediaFile, err := storage.Reader(screen.mediafile)
		if err != nil {
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}

		// Copy to temp file for http.ServeContent (needs io.ReadSeeker)
		ext := filepath.Ext(screen.MediaText.Text)
		tempFile, err := os.CreateTemp("", "go2tv-*"+ext)
		if err != nil {
			mediaFile.Close()
			check(w, fmt.Errorf("temp file create: %w", err))
			startAfreshPlayButton(screen)
			return
		}

		if _, err := io.Copy(tempFile, mediaFile); err != nil {
			mediaFile.Close()
			tempFile.Close()
			os.Remove(tempFile.Name())
			check(w, fmt.Errorf("temp file copy: %w", err))
			startAfreshPlayButton(screen)
			return
		}
		mediaFile.Close()
		tempFile.Close()

		tempFilePath := tempFile.Name()

		// Add media handler with temp file path (string type triggers os.Open in handler)
		mediaFilename := "/" + utils.ConvertFilename(screen.MediaText.Text)
		screen.httpserver.AddHandler(mediaFilename, nil, nil, tempFilePath)

		serverStarted := make(chan error)
		go func() {
			screen.httpserver.StartServing(serverStarted)
			// Clean up temp file when server stops
			os.Remove(tempFilePath)
			serverCTXStop()
		}()

		if err := <-serverStarted; err != nil {
			os.Remove(tempFilePath)
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}

		mediaURL = "http://" + whereToListen + mediaFilename
	}

	// Handle subtitles
	var subtitleURL string
	if screen.subsfile != nil && screen.httpserver != nil {
		mediaURLParsed, err := url.Parse(mediaURL)
		if err == nil && mediaURLParsed.Host != "" {
			ext := strings.ToLower(filepath.Ext(screen.SubsText.Text))
			switch ext {
			case ".srt":
				subsReader, err := storage.Reader(screen.subsfile)
				if err == nil {
					webvttData, err := utils.ConvertSRTReaderToWebVTT(subsReader)
					subsReader.Close()
					if err == nil {
						screen.httpserver.AddHandler("/subtitles.vtt", nil, nil, webvttData)
						subtitleURL = "http://" + mediaURLParsed.Host + "/subtitles.vtt"
					}
				}
			case ".vtt":
				subsReader, err := storage.Reader(screen.subsfile)
				if err == nil {
					subsData, err := io.ReadAll(subsReader)
					subsReader.Close()
					if err == nil {
						screen.httpserver.AddHandler("/subtitles.vtt", nil, nil, subsData)
						subtitleURL = "http://" + mediaURLParsed.Host + "/subtitles.vtt"
					}
				}
			}
		}
	}

	// Load media (duration=0 since mobile doesn't support transcoding)
	// Use LIVE stream type for URL streams (DMR shows LIVE badge, but buffer unchanged)
	go func() {
		live := screen.ExternalMediaURL.Checked
		if err := client.Load(mediaURL, mediaType, 0, 0, subtitleURL, live); err != nil {
			check(w, fmt.Errorf("chromecast load: %w", err))
			startAfreshPlayButton(screen)
			return
		}
	}()

	go chromecastStatusWatcher(serverStoppedCTX, screen)
}

// chromecastStatusWatcher polls Chromecast status and updates UI.
func chromecastStatusWatcher(ctx context.Context, screen *FyneScreen) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var mediaStarted bool

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

			switch status.PlayerState {
			case "BUFFERING":
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
				if mediaStarted {
					screen.Fini()
					if !screen.Medialoop {
						startAfreshPlayButton(screen)
					}
					return
				}
			}

			// Fallback: Detect media completion when CurrentTime reaches Duration
			if mediaStarted && status.Duration > 0 && status.CurrentTime >= status.Duration-1.5 {
				screen.Fini()
				if !screen.Medialoop {
					startAfreshPlayButton(screen)
				}
				return
			}
		}
	}
}
