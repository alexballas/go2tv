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

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/storage/repository"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/pkg/errors"
	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

func chromecastMediaTitle(screen *FyneScreen, fallback string) string {
	if screen == nil {
		return fallback
	}
	if screen.MediaText != nil {
		if title := strings.TrimSpace(screen.MediaText.Text); title != "" {
			return title
		}
	}
	if screen.mediafile != nil {
		if title := strings.TrimSpace(screen.mediafile.Name()); title != "" {
			return title
		}
	}
	return fallback
}

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
	var resumeHotkeys func()
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if resumeHotkeys != nil {
			defer resumeHotkeys()
		}
		check(w, err)

		if reader == nil {
			return
		}

		defer reader.Close()

		screen.MediaText.Text = reader.URI().Name()
		screen.mediafile = reader.URI()
		resolveSelectedMobileArtwork(screen, reader.URI())

		screen.MediaText.Refresh()
	}, w)

	fd.SetFilter(storage.NewExtensionFileFilter(screen.mediaFormats))

	resumeHotkeys = suspendHotkeys(screen)
	fd.Show()
}

func resolveSelectedMobileArtwork(screen *FyneScreen, mediaURI fyne.URI) {
	identity := mobileGUIArtworkIdentity(mediaURI)
	screen.setCurrentArtworkTarget(identity)
	if identity == "" {
		return
	}
	go func() {
		mediaReader, err := storage.Reader(mediaURI)
		if err != nil {
			screen.setResolvedCurrentArtwork(identity, nil)
			return
		}
		mediaType, err := utils.GetMimeDetailsFromStream(mediaReader)
		mediaReader.Close()
		if err != nil {
			screen.setResolvedCurrentArtwork(identity, nil)
			return
		}
		asset := resolveMobileGUIArtwork(mediaURI, mediaType, nil)
		screen.setResolvedCurrentArtwork(identity, asset)
	}()
}

func subsAction(screen *FyneScreen) {
	w := screen.Current
	var resumeHotkeys func()
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if resumeHotkeys != nil {
			defer resumeHotkeys()
		}
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

	resumeHotkeys = suspendHotkeys(screen)
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
		if currentState == "Playing" {
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
		if currentState == "Playing" {
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
		actionID := screen.nextChromecastActionID()
		go chromecastPlayAction(screen, actionID)
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
		case "PAUSED_PLAYBACK":
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
		// http.ServeContent needs an io.ReadSeeker for range requests. We try
		// storage.ReaderSeeker first (a real seekable handle, no copy) and fall
		// back to a temp file copy when the platform can't provide one. See
		// seekableMediaForCasting.
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
			// Video/Audio: serve a seekable reader directly when possible,
			// falling back to a temp file copy otherwise.
			mediaFile, err = seekableMediaForCasting(screen)
			if err != nil {
				check(w, err)
				startAfreshPlayButton(screen)
				return
			}
			if !screen.ExternalMediaURL.Checked {
				screen.resolveCurrentMobileGUIArtwork(screen.mediafile, mediaType, mediaFile)
			}
		}
	}

	if screen.ExternalMediaURL.Checked {
		screen.setCurrentArtwork(nil)
		// We're not using any context here. The reason is
		// that when the webserver shuts down it causes the
		// the io.Copy operation to fail with "broken pipe".
		// That's good enough for us since right after that
		// we close the io.ReadCloser.
		mediaURL, inferredMediaType, err := utils.StreamURLWithMime(context.Background(), screen.MediaText.Text)
		check(screen.Current, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		mediaType = inferredMediaType

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

	transcodeEnabled := mediaTranscodeEnabled(screen, mediaType)

	// Non-transcoded local media is served with HTTP range support (see
	// seekableMediaForCasting), so advertise it as seekable and let the renderer
	// seek via its own controls. External URLs are streamed without range support
	// and transcoded streams are live, so both stay non-seekable.
	isSeek := !transcodeEnabled && !screen.ExternalMediaURL.Checked

	ffmpegSubsPath := ""
	if screen.subsfile != nil {
		if transcodeEnabled {
			ffmpegSubsPath, err = copySubsToTempFile(screen)
			check(screen.Current, err)
			if err != nil {
				startAfreshPlayButton(screen)
				return
			}
		} else {
			subsFile, err = storage.Reader(screen.subsfile)
			check(screen.Current, err)
			if err != nil {
				startAfreshPlayButton(screen)
				return
			}
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
		Transcode:                   transcodeEnabled,
		Seekable:                    isSeek,
		LogOutput:                   screen.Debug,
		FFmpegPath:                  screen.ffmpegPath,
		FFmpegSubsPath:              ffmpegSubsPath,
	}

	screen.httpserver = httphandlers.NewServer(whereToListen)
	artworkAsset := screen.getCurrentArtwork()
	registerGUIArtwork(screen.httpserver, artworkAsset)
	screen.tvdata.Metadata = guiMediaMetadata("", whereToListen, artworkAsset)
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
		fyne.Do(func() {
			lsize := screen.DeviceList.Length()
			for i := 0; i <= lsize; i++ {
				screen.DeviceList.Unselect(lsize - 1)
			}
			screen.controlURL = ""
		})
		stopAction(screen)
	}
}

func clearmediaAction(screen *FyneScreen) {
	screen.MediaText.SetText("")
	screen.mediafile = nil
	screen.setCurrentArtwork(nil)
}

func clearsubsAction(screen *FyneScreen) {
	screen.SubsText.SetText("")
	screen.subsfile = nil
	removeTempFile(&screen.tempSubsFile)
}

func isImageMediaType(mediaType string) bool {
	return strings.Contains(strings.ToLower(mediaType), "image")
}

func isAudioMediaType(mediaType string) bool {
	return strings.HasPrefix(strings.ToLower(mediaType), "audio/")
}

// mediaTranscodeEnabled reports whether the Transcode option applies to the
// selected media. Images are never transcoded; the checkbox is unchecked so
// the user can see the option was ignored.
func mediaTranscodeEnabled(screen *FyneScreen, mediaType string) bool {
	if !screen.Transcode {
		return false
	}

	if isImageMediaType(mediaType) {
		disableTranscodeForImage(screen)
		return false
	}

	return true
}

func disableTranscodeForImage(screen *FyneScreen) {
	screen.Transcode = false
	fyne.Do(func() {
		if screen.TranscodeCheckBox != nil && screen.TranscodeCheckBox.Checked {
			screen.TranscodeCheckBox.SetChecked(false)
		}
	})
}

func copySubsToTempFile(screen *FyneScreen) (string, error) {
	removeTempFile(&screen.tempSubsFile)

	subsReader, err := storage.Reader(screen.subsfile)
	if err != nil {
		return "", err
	}
	defer subsReader.Close()

	ext := filepath.Ext(screen.SubsText.Text)
	if ext == "" {
		ext = ".srt"
	}

	tempFile, err := createMobileCacheTemp("go2tv-sub-*" + ext)
	if err != nil {
		return "", fmt.Errorf("temp subtitle create: %w", err)
	}

	if _, err := io.Copy(tempFile, subsReader); err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("temp subtitle copy: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("temp subtitle close: %w", err)
	}

	screen.tempSubsFile = tempFile.Name()
	return screen.tempSubsFile, nil
}

func mobileTranscodeOptions(screen *FyneScreen) (*utils.TranscodeOptions, error) {
	subsPath := ""
	if screen.subsfile != nil {
		var err error
		subsPath, err = copySubsToTempFile(screen)
		if err != nil {
			return nil, err
		}
	}

	return &utils.TranscodeOptions{
		FFmpegPath:   screen.ffmpegPath,
		SubsPath:     subsPath,
		SubtitleSize: utils.SubtitleSizeMedium,
		LogOutput:    screen.Debug,
	}, nil
}

// startChromecastMediaServer (re)starts the local HTTP server that serves the
// media to the Chromecast. It returns the served media URL together with a
// context that is cancelled once the server stops.
func startChromecastMediaServer(screen *FyneScreen, mediaFilename string, tcOpts *utils.TranscodeOptions, media any, artworkAsset *metadata.ArtworkAsset) (string, context.Context, error) {
	whereToListen, err := utils.URLtoListenIPandPort(screen.selectedDevice.addr)
	if err != nil {
		return "", nil, err
	}

	if screen.httpserver != nil {
		screen.httpserver.StopServer()
	}

	screen.httpserver = httphandlers.NewServer(whereToListen)
	registerGUIArtwork(screen.httpserver, artworkAsset)
	serverStoppedCTX, serverCTXStop := context.WithCancel(context.Background())
	screen.serverStopCTX = serverStoppedCTX
	screen.cancelServerStop = serverCTXStop

	screen.httpserver.AddHandler(mediaFilename, nil, tcOpts, media)

	serverStarted := make(chan error)
	go func() {
		screen.httpserver.StartServing(serverStarted)
		serverCTXStop()
	}()

	if err := <-serverStarted; err != nil {
		return "", nil, err
	}

	return "http://" + whereToListen + mediaFilename, serverStoppedCTX, nil
}

func startChromecastSubtitleServer(screen *FyneScreen) (string, context.Context, error) {
	whereToListen, err := utils.URLtoListenIPandPort(screen.selectedDevice.addr)
	if err != nil {
		return "", nil, err
	}

	if screen.httpserver != nil {
		screen.httpserver.StopServer()
	}

	screen.httpserver = httphandlers.NewServer(whereToListen)
	serverStoppedCTX, serverCTXStop := context.WithCancel(context.Background())
	screen.serverStopCTX = serverStoppedCTX
	screen.cancelServerStop = serverCTXStop

	serverStarted := make(chan error)
	go func() {
		screen.httpserver.StartServing(serverStarted)
		serverCTXStop()
	}()

	if err := <-serverStarted; err != nil {
		return "", nil, err
	}

	return whereToListen, serverStoppedCTX, nil
}

func hasChromecastMobileSubtitles(screen *FyneScreen) bool {
	if screen.subsfile == nil {
		return false
	}

	switch strings.ToLower(filepath.Ext(screen.SubsText.Text)) {
	case ".srt", ".vtt":
		return true
	default:
		return false
	}
}

func removeTempFile(path *string) {
	if *path == "" {
		return
	}

	os.Remove(*path)
	*path = ""
}

func stopAction(screen *FyneScreen) {
	screen.nextChromecastActionID()

	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")

	// Clear casting media type immediately
	screen.SetMediaType("")

	// Clean up temp files
	removeTempFile(&screen.tempMediaFile)
	removeTempFile(&screen.tempSubsFile)

	// Handle Chromecast stop
	if screen.chromecastClient != nil && screen.chromecastClient.IsConnected() {
		client := screen.chromecastClient
		server := screen.httpserver

		screen.chromecastClient = nil
		screen.httpserver = nil

		go func() {
			_ = client.Stop()
			client.Close(false)
			if server != nil {
				server.StopServer()
			}
		}()
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
		server := screen.httpserver
		screen.tvdata = nil
		screen.httpserver = nil

		if tvdata != nil && tvdata.ControlURL != "" {
			_ = tvdata.SendtoTV("Stop")
		}
		if server != nil {
			server.StopServer()
		}
	}()
}

func getDevices() ([]devType, error) {
	deviceList, err := devices.LoadAllDevices()
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

// seekableMediaForCasting returns a value for the HTTP server's media handler.
// When the platform can provide a real seekable handle (e.g. an Android
// content:// file descriptor via storage.ReaderSeeker), it returns a factory
// that opens a fresh seekable reader per request, so http.ServeContent can
// satisfy range requests without copying the file. Otherwise it falls back to
// copying the media to a temp file (recorded in screen.tempMediaFile for
// cleanup in stopAction) and returns that path.
func seekableMediaForCasting(screen *FyneScreen) (any, error) {
	uri := screen.mediafile

	// Fast path: a real seekable handle is available. Probe once, then open a
	// fresh reader per request (each HTTP request needs its own read offset).
	if rs, err := storage.ReaderSeeker(uri); err == nil {
		rs.Close()
		return httphandlers.MediaReaderSeeker(func() (io.ReadSeekCloser, error) {
			return storage.ReaderSeeker(uri)
		}), nil
	} else if !errors.Is(err, repository.ErrOperationNotSupported) {
		return nil, err
	}

	// Fallback: copy to a temp file we can serve as a seekable os.File.
	mediaReader, err := storage.Reader(uri)
	if err != nil {
		return nil, err
	}
	defer mediaReader.Close()

	ext := filepath.Ext(screen.MediaText.Text)
	tempFile, err := createMobileCacheTemp("go2tv-*" + ext)
	if err != nil {
		return nil, fmt.Errorf("temp file create: %w", err)
	}

	if _, err := io.Copy(tempFile, mediaReader); err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return nil, fmt.Errorf("temp file copy: %w", err)
	}
	tempFile.Close()

	screen.tempMediaFile = tempFile.Name()
	return screen.tempMediaFile, nil
}

func startAfreshPlayButton(screen *FyneScreen) {
	screen.nextChromecastActionID()

	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}

	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")
}

// chromecastPlayAction handles playback on Chromecast devices.
// Supports both local files (via internal HTTP server) and external URLs (direct).
func chromecastPlayAction(screen *FyneScreen, actionID uint64) {
	if !screen.isChromecastActionCurrent(actionID) {
		return
	}

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
	var artworkAsset *metadata.ArtworkAsset

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

		// Note: Debug logging disabled on mobile.
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
	var transcode bool
	serverStoppedCTX := context.Background()
	subtitleHost := ""
	screen.mediaDuration = 0

	if screen.ExternalMediaURL.Checked {
		screen.setCurrentArtwork(nil)
		mediaURL = screen.MediaText.Text

		mediaURLinfo, inferredMediaType, err := utils.StreamURLWithMime(context.Background(), mediaURL)
		if err != nil {
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}
		mediaType = inferredMediaType
		mediaURLinfo.Close()

		wasTranscode := screen.Transcode
		transcode = playback.ChromecastTranscodeEnabled(mediaTranscodeEnabled(screen, mediaType), mediaURL, mediaType)
		if wasTranscode && !transcode {
			screen.Transcode = false
			fyne.Do(func() {
				if screen.TranscodeCheckBox != nil && screen.TranscodeCheckBox.Checked {
					screen.TranscodeCheckBox.SetChecked(false)
				}
			})
		}

		screen.SetMediaType(mediaType)

		if screen.selectedDevice.isAudioOnly && (strings.Contains(mediaType, "video") || strings.Contains(mediaType, "image")) {
			check(w, errors.New(lang.L("Video/Image file not supported by audio-only device")))
			startAfreshPlayButton(screen)
			return
		}

		if transcode {
			stream, err := utils.StreamURL(context.Background(), mediaURL)
			if err != nil {
				check(w, err)
				startAfreshPlayButton(screen)
				return
			}

			tcOpts, err := mobileTranscodeOptions(screen)
			if err != nil {
				stream.Close()
				check(w, err)
				startAfreshPlayButton(screen)
				return
			}

			servedURL, serverCTX, err := startChromecastMediaServer(screen, "/"+utils.ConvertFilename(mediaURL), tcOpts, stream, artworkAsset)
			if err != nil {
				stream.Close()
				check(w, err)
				startAfreshPlayButton(screen)
				return
			}

			serverStoppedCTX = serverCTX
			mediaURL = servedURL
			mediaType = "video/mp4"
		} else {
			if hasChromecastMobileSubtitles(screen) {
				host, serverCTX, err := startChromecastSubtitleServer(screen)
				if err != nil {
					check(w, err)
					startAfreshPlayButton(screen)
					return
				}

				subtitleHost = host
				serverStoppedCTX = serverCTX
			} else {
				if screen.httpserver != nil {
					screen.httpserver.StopServer()
					screen.httpserver = nil
				}

				var cancel context.CancelFunc
				serverStoppedCTX, cancel = context.WithCancel(context.Background())
				screen.serverStopCTX = serverStoppedCTX
				screen.cancelServerStop = cancel
			}
		}

	} else {
		// LOCAL FILE: Serve via internal HTTP server.
		// http.ServeContent needs an io.ReadSeeker for range requests; we serve
		// a seekable reader directly when available and fall back to a temp file
		// copy otherwise (see seekableMediaForCasting).
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

		transcode = mediaTranscodeEnabled(screen, mediaType) && !isAudioMediaType(mediaType)

		screen.SetMediaType(mediaType)

		if screen.selectedDevice.isAudioOnly && (strings.Contains(mediaType, "video") || strings.Contains(mediaType, "image")) {
			check(w, errors.New(lang.L("Video/Image file not supported by audio-only device")))
			startAfreshPlayButton(screen)
			return
		}

		// Serve a seekable reader directly when possible, falling back to a
		// temp file copy otherwise (cleaned up via screen.tempMediaFile).
		media, err := seekableMediaForCasting(screen)
		if err != nil {
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}
		artworkAsset = screen.resolveCurrentMobileGUIArtwork(screen.mediafile, mediaType, media)

		var tcOpts *utils.TranscodeOptions
		if transcode {
			tcOpts, err = mobileTranscodeOptions(screen)
			if err != nil {
				check(w, err)
				startAfreshPlayButton(screen)
				return
			}
			if mediaPath, ok := media.(string); ok {
				if duration, err := utils.DurationForMediaSeconds(screen.ffmpegPath, mediaPath); err == nil {
					screen.mediaDuration = duration
				}
			}
			mediaType = "video/mp4"
		}

		servedURL, serverCTX, err := startChromecastMediaServer(screen, "/"+utils.ConvertFilename(screen.MediaText.Text), tcOpts, media, artworkAsset)
		if err != nil {
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}

		serverStoppedCTX = serverCTX
		mediaURL = servedURL
	}

	// Handle subtitles
	var subtitleURL string
	if hasChromecastMobileSubtitles(screen) && screen.httpserver != nil && !transcode {
		if subtitleHost == "" {
			mediaURLParsed, err := url.Parse(mediaURL)
			if err == nil {
				subtitleHost = mediaURLParsed.Host
			}
		}
		if subtitleHost != "" {
			ext := strings.ToLower(filepath.Ext(screen.SubsText.Text))
			switch ext {
			case ".srt":
				subsReader, err := storage.Reader(screen.subsfile)
				if err == nil {
					webvttData, err := utils.ConvertSRTReaderToWebVTT(subsReader)
					subsReader.Close()
					if err == nil {
						screen.httpserver.AddHandler("/subtitles.vtt", nil, nil, webvttData)
						subtitleURL = "http://" + subtitleHost + "/subtitles.vtt"
					}
				}
			case ".vtt":
				subsReader, err := storage.Reader(screen.subsfile)
				if err == nil {
					subsData, err := io.ReadAll(subsReader)
					subsReader.Close()
					if err == nil {
						screen.httpserver.AddHandler("/subtitles.vtt", nil, nil, subsData)
						subtitleURL = "http://" + subtitleHost + "/subtitles.vtt"
					}
				}
			}
		}
	}

	// Use LIVE stream type for URL streams (DMR shows LIVE badge, but buffer unchanged)
	go func() {
		live := screen.ExternalMediaURL.Checked
		listenAddress := ""
		if parsedMediaURL, err := url.Parse(mediaURL); err == nil {
			listenAddress = parsedMediaURL.Host
		}
		if err := client.LoadMedia(castprotocol.LoadRequest{
			MediaURL:    mediaURL,
			ContentType: mediaType,
			Metadata:    guiMediaMetadata(chromecastMediaTitle(screen, mediaURL), listenAddress, artworkAsset),
			Duration:    screen.mediaDuration,
			SubtitleURL: subtitleURL,
			Live:        live,
		}); err != nil {
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}
			check(w, fmt.Errorf("chromecast load: %w", err))
			startAfreshPlayButton(screen)
			return
		}
	}()

	go chromecastStatusWatcher(serverStoppedCTX, screen, actionID)
}

const (
	// chromecastStallWindowSeconds scopes the stall safety net to the tail
	// of the media, where a wedged session is indistinguishable from a
	// finished one.
	chromecastStallWindowSeconds = 5.0
	// chromecastStallTicksPlaying is the poll count before a frozen PLAYING
	// position near the end is treated as finished. A genuinely playing
	// video advances on every poll, so this can act fast.
	chromecastStallTicksPlaying = 3
	// chromecastStallTicksWedged is the poll count before any other
	// non-advancing near-end state (e.g. a BUFFERING that never receives
	// more data, or a session that stopped reporting a duration) is treated
	// as finished. Higher than the PLAYING threshold so a slow network
	// cannot cut a video short.
	chromecastStallTicksWedged = 10
	// chromecastLostConnPolls is the consecutive GetStatus failure count
	// before the connection to the device is considered dead. Each failure
	// already went through the library's internal retries, so this
	// represents an extended period without any response.
	chromecastLostConnPolls = 3
	// chromecastStartupIdleTicks is the poll count before a new Chromecast
	// action that never leaves pre-playback IDLE is considered wedged.
	chromecastStartupIdleTicks = 20
)

// chromecastStatusWatcher polls Chromecast status and updates UI.
func chromecastStatusWatcher(ctx context.Context, screen *FyneScreen, actionID uint64) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var mediaStarted bool

	// Stall detection state for the near-end safety net below.
	var stallLastTime float32 = -1
	var stallDuration float32
	stallTicks := 0

	// Consecutive GetStatus failures, see the dead-connection handling.
	statusErrs := 0
	startupIdleTicks := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}
			// Capture client once to avoid race with stopAction nilling it
			client := screen.chromecastClient
			if client == nil || !client.IsConnected() {
				return
			}

			status, err := client.GetStatus()
			if err != nil {
				statusErrs++
				if statusErrs < chromecastLostConnPolls {
					continue
				}
				if !screen.isChromecastActionCurrent(actionID) {
					return
				}
				nearEnd := mediaStarted &&
					stallLastTime >= 0 && stallDuration > 0 &&
					stallLastTime >= stallDuration-chromecastStallWindowSeconds

				// Drop the dead client so follow-up actions reconnect
				// from scratch instead of reusing it.
				if screen.chromecastClient == client {
					screen.chromecastClient = nil
				}
				go client.Close(false)

				if nearEnd {
					// Last good sample was already at EOF, so a dead status
					// channel is equivalent to the receiver never sending IDLE.
					screen.Fini()
					if !screen.Medialoop {
						startAfreshPlayButton(screen)
					}
					return
				}

				// Mid-media status loss is not completion.
				if screen.httpserver != nil {
					server := screen.httpserver
					screen.httpserver = nil
					go server.StopServer()
				}
				if screen.cancelServerStop != nil {
					screen.cancelServerStop()
					screen.cancelServerStop = nil
				}
				startAfreshPlayButton(screen)
				return
			}
			statusErrs = 0
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}

			switch status.PlayerState {
			case "BUFFERING":
				// Media is loading; wait for PLAYING/PAUSED before treating
				// later IDLE/status-loss as a finished playback.
				startupIdleTicks = 0
			case "PLAYING":
				mediaStarted = true
				startupIdleTicks = 0
				if screen.getScreenState() != "Playing" {
					// Double check to avoid a race condition when clicking the stop button
					if client.IsConnected() {
						setPlayPauseView("Pause", screen)
						screen.updateScreenState("Playing")
					}
				}
			case "PAUSED":
				mediaStarted = true
				startupIdleTicks = 0
				if screen.getScreenState() != "Paused" {
					setPlayPauseView("Play", screen)
					screen.updateScreenState("Paused")
				}
			case "IDLE":
				if mediaStarted {
					if !screen.isChromecastActionCurrent(actionID) {
						return
					}
					screen.Fini()
					if !screen.Medialoop {
						startAfreshPlayButton(screen)
					}
					return
				}
				startupIdleTicks++
				if startupIdleTicks >= chromecastStartupIdleTicks {
					if !screen.isChromecastActionCurrent(actionID) {
						return
					}
					if screen.chromecastClient == client {
						screen.chromecastClient = nil
					}
					go client.Close(false)
					if screen.httpserver != nil {
						server := screen.httpserver
						screen.httpserver = nil
						go server.StopServer()
					}
					if screen.cancelServerStop != nil {
						screen.cancelServerStop()
						screen.cancelServerStop = nil
					}
					if screen.Medialoop {
						screen.Fini()
						return
					}
					startAfreshPlayButton(screen)
					return
				}
			}

			// Near-end stall safety net: natural completion is detected via
			// the IDLE state above once the receiver tears the session down.
			// This catches sessions that wedge close to the end of the
			// stream instead: a frozen PLAYING position, a BUFFERING state
			// that never receives more data, or a session that stops
			// reporting a duration. A healthy video advances on every poll.
			if !mediaStarted {
				continue
			}
			if status.PlayerState == "PAUSED" {
				// A paused video legitimately holds its position.
				stallTicks = 0
				continue
			}
			// Chromecast reports 0 duration/time during buffering, so only
			// non-buffering reports with a duration carry a usable position.
			sampleValid := status.PlayerState != "BUFFERING" && status.Duration > 0
			if sampleValid && status.CurrentTime != stallLastTime {
				// Progress (or a seek): remember the latest good sample.
				stallLastTime = status.CurrentTime
				stallDuration = status.Duration
				stallTicks = 0
				continue
			}
			if stallLastTime < 0 || stallDuration <= 0 || stallLastTime < stallDuration-chromecastStallWindowSeconds {
				continue
			}
			stallTicks++
			// A frozen PLAYING report is unambiguous, so act fast. Anything
			// else can also be a slow network, so give it more time before
			// treating it as finished.
			threshold := chromecastStallTicksWedged
			if sampleValid && status.PlayerState == "PLAYING" {
				threshold = chromecastStallTicksPlaying
			}
			if stallTicks < threshold {
				continue
			}
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}
			screen.Fini()
			if !screen.Medialoop {
				startAfreshPlayButton(screen)
			}
			return
		}
	}
}
