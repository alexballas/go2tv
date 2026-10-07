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
	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/mediasource"
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

func selectedMobileChromecastControlClient(screen *FyneScreen, device devType) (*castprotocol.CastClient, func(), error) {
	if device.deviceType != devices.DeviceTypeChromecast || device.addr == "" {
		return nil, nil, errors.New("chromecast device not selected")
	}

	if client := screen.chromecastClient; client != nil && client.IsConnected() && chromecastClientOwnsDevice(client, device) {
		return client, func() {}, nil
	}

	client, err := castprotocol.NewCastClient(device.addr)
	if err != nil {
		return nil, nil, err
	}
	client.LogOutput = screen.Debug
	if err := client.Connect(); err != nil {
		_ = client.Close(false)
		return nil, nil, err
	}

	return client, func() { _ = client.Close(false) }, nil
}

func muteAction(screen *FyneScreen) {
	w := screen.Current
	selectedDevice := screen.selectedDevice

	// Query the selected Chromecast instead of trusting the icon: the icon may
	// still describe an active cast after the user selects another device.
	if selectedDevice.deviceType == devices.DeviceTypeChromecast {
		go func() {
			client, cleanup, err := selectedMobileChromecastControlClient(screen, selectedDevice)
			if err != nil {
				check(w, errors.New(lang.L("chromecast not connected")))
				return
			}
			defer cleanup()

			status, err := client.GetStatus()
			if err != nil {
				check(w, errors.New(lang.L("could not send mute action")))
				return
			}
			muted := !status.Muted
			if err := client.SetMuted(muted); err != nil {
				check(w, errors.New(lang.L("could not send mute action")))
				return
			}
			if muted {
				setMuteUnmuteView("Unmute", screen)
				return
			}
			setMuteUnmuteView("Mute", screen)
		}()
		return
	}

	// Handle icon toggle (mute -> unmute)
	if screen.MuteUnmute.Icon == theme.VolumeMuteIcon() {
		unmuteAction(screen)
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

		if isTorrentDocument(reader.URI(), "") {
			// The torrent dialog's picker supports provider-backed documents.
			showTorrentDocument(screen, reader)
			return
		}
		defer reader.Close()
		setMobileMediaURI(screen, reader.URI())
	}, w)

	fd.SetFilter(storage.NewExtensionFileFilter(append(append([]string(nil), screen.mediaFormats...), ".torrent")))

	resumeHotkeys = suspendHotkeys(screen)
	fd.Show()
}

// setMobileMediaURI selects uri as the media file. Both ways of choosing one -
// the file picker above and a share from another app - go through here so they
// cannot drift apart. Must be called on the Fyne goroutine.
func setMobileMediaURI(screen *FyneScreen, uri fyne.URI) {
	if isTorrentDocument(uri, "") {
		openMobileTorrentDocument(screen, uri)
		return
	}
	if torrentMediaSelected(screen) {
		cancelTorrent(screen)
	}
	screen.MediaText.Text = uri.Name()
	screen.mediafile = uri
	resolveSelectedMobileArtwork(screen, uri)

	screen.MediaText.Refresh()
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

	fd.SetFilter(storage.NewExtensionFileFilter(mediamodel.SubtitleExtensions()))

	resumeHotkeys = suspendHotkeys(screen)
	fd.Show()
}

func playAction(screen *FyneScreen) {
	var claimed bool
	fyne.DoAndWait(func() { claimed = claimMobilePlayback(screen) })
	if claimed {
		playMobileAction(screen)
	}
}

func playMobileAction(screen *FyneScreen) {
	finishStartup := func() {}
	defer func() {
		finishMobilePlayback(screen)
		finishStartup()
	}()
	// Selecting replacement media queues cancellation on the UI goroutine.
	// Finish that teardown before creating or controlling another cast.
	if done := screen.torrentCancellationDone(); done != nil {
		<-done
	}
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
	if client := screen.chromecastClient; client != nil && client.IsConnected() && isActivePlayback {
		// The screen state can be stale (a session that died or finished
		// while unobserved), so toggle on the device's live state instead
		// of writing a command into a possibly dead socket.
		status, err := client.GetStatus()
		if err != nil {
			dropDeadChromecastSession(screen, client)
			return
		}
		switch status.PlayerState {
		case "PLAYING", "BUFFERING":
			if err := client.Pause(); err != nil {
				check(w, err)
				return
			}
			setPlayPauseView("Play", screen)
			screen.updateScreenState("Paused")
		case "PAUSED":
			if err := client.Play(); err != nil {
				check(w, err)
				return
			}
			setPlayPauseView("Pause", screen)
			screen.updateScreenState("Playing")
		default:
			// IDLE: playback ended while the UI still showed a session.
			dropDeadChromecastSession(screen, client)
		}
		return
	}

	// Branch based on device type - MUST be first, before any DLNA-specific logic
	var startupCtx context.Context
	var mediaPath string
	for {
		if done := screen.torrentCancellationDone(); done != nil {
			<-done
		}
		mediaPath = ""
		if screen.mediafile != nil && !screen.ExternalMediaURL.Checked {
			mediaPath = screen.mediafile.Path()
		}
		var starting bool
		startupCtx, finishStartup, starting = screen.beginPlaybackStartup(mediaPath)
		if starting {
			break
		}
		// Cancellation may have been queued between waiting and registration.
		if screen.torrentCancellationDone() == nil {
			finishStartup = func() {}
			return
		}
	}
	if startupCtx.Err() != nil {
		return
	}
	setPlayPauseView("", screen)
	streamCtx, detachStream := playbackStreamContext(startupCtx)
	defer detachStream()
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		actionID := screen.nextChromecastActionID()
		chromecastPlayAction(screen, actionID, startupCtx)
		return
	}

	existingSeek := 0
	if screen.dlnaSeekRestart {
		existingSeek = screen.ffmpegSeek
	}
	screen.dlnaSeekRestart = false
	screen.ffmpegSeek = existingSeek
	sessionDevice := screen.selectedDevice

	// DLNA timeout mechanism - re-enable play button if no response after 5 seconds
	screen.cancelPlayTimer()

	ctx, cancelEnablePlay := context.WithTimeout(context.Background(), 5*time.Second)
	screen.mu.Lock()
	screen.cancelEnablePlay = cancelEnablePlay
	screen.mu.Unlock()

	go func() {
		<-ctx.Done()

		defer cancelEnablePlay()

		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}
		if screen.tvdata == nil {
			return
		}

		out, err := screen.tvdata.GetTransportInfo()
		if err != nil {
			return
		}

		switch out[0] {
		case "PLAYING":
			screen.updateScreenState("Playing")
			setPlayPauseView("Pause", screen)
		case "PAUSED_PLAYBACK":
			screen.updateScreenState("Paused")
			setPlayPauseView("Play", screen)
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
		mediaType, err = mobileMediaMIMEContext(startupCtx, screen.mediafile)
		if startupCtx.Err() != nil {
			return
		}
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
			if !screen.ExternalMediaURL.Checked && !torrentMediaSelected(screen) {
				screen.resolveCurrentMobileGUIArtwork(screen.mediafile, mediaType, mediaFile)
			}
		}
	}

	if screen.ExternalMediaURL.Checked {
		screen.setCurrentArtwork(nil)
		mediaURL, inferredMediaType, err := utils.StreamURLWithMime(streamCtx, screen.MediaText.Text)
		if startupCtx.Err() != nil {
			if mediaURL != nil {
				_ = mediaURL.Close()
			}
			return
		}
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
			if startupCtx.Err() != nil {
				return
			}
			if err != nil {
				startAfreshPlayButton(screen)
				return
			}
			mediaFile = readerToBytes
		}
	}

	transcodeEnabled := mediaTranscodeEnabled(screen, mediaType)
	storedResume := screen.prepareResumeSession(mediaType)
	var directResumeSeek int
	screen.ffmpegSeek, directResumeSeek = computeResumeStart(existingSeek, storedResume, transcodeEnabled)

	// Non-transcoded local media is served with HTTP range support (see
	// seekableMediaForCasting), so advertise it as seekable and let the renderer
	// seek via its own controls. External URLs and live transcoded streams lack
	// renderer-side range support.
	isSeek := !transcodeEnabled && !screen.ExternalMediaURL.Checked
	mediaDuration := 0.0
	if transcodeEnabled {
		switch media := mediaFile.(type) {
		case string:
			mediaDuration, _ = utils.DurationForMediaSecondsContext(startupCtx, screen.ffmpegPath, media)
		case httphandlers.MediaReaderSeeker:
			if reader, openErr := media(); openErr == nil {
				mediaDuration, _ = utils.DurationForMediaReaderSeconds(startupCtx, screen.ffmpegPath, reader)
				_ = reader.Close()
			}
		}
	}
	if startupCtx.Err() != nil {
		return
	}
	screen.mediaDuration = mediaDuration
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
		MediaPath:                   mediaPath,
		CurrentTimers:               make(map[string]*time.Timer),
		MediaRenderersStates:        make(map[string]*soapcalls.States),
		InitialMediaRenderersStates: make(map[string]bool),
		Transcode:                   transcodeEnabled,
		Seekable:                    isSeek,
		MediaDuration:               mediaDuration,
		LogOutput:                   screen.Debug,
		FFmpegPath:                  screen.ffmpegPath,
		FFmpegSeek:                  screen.ffmpegSeek,
		FFmpegSubsPath:              ffmpegSubsPath,
		TorrentSource:               mobileTorrentSubtitleSource(screen),
	}
	screen.tvdata.SetContext(streamCtx)
	showDLNATranscodeTimeline(screen, screen.tvdata)

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
	if startupCtx.Err() != nil {
		return
	}
	check(w, err)
	if err != nil {
		stopAction(screen)
		return
	}

	err = screen.tvdata.SendtoTV("Play1")
	if startupCtx.Err() != nil {
		return
	}
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
		return
	}
	screen.setActiveDevice(sessionDevice)
	screen.applyInitialDLNAResume(screen.tvdata, directResumeSeek)
	screen.updateScreenState("Playing")
	setPlayPauseView("Pause", screen)
}

func clearmediaAction(screen *FyneScreen) {
	if torrentMediaSelected(screen) {
		cancelTorrent(screen)
	}
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
	ctx := screen.playbackStartupContext()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	removeTempFile(&screen.tempSubsFile)

	subsReader, err := storage.Reader(screen.subsfile)
	if err != nil {
		return "", err
	}
	defer subsReader.Close()
	stopClosing := context.AfterFunc(ctx, func() { _ = subsReader.Close() })
	defer stopClosing()

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
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("temp subtitle copy: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("temp subtitle close: %w", err)
	}
	if err := ctx.Err(); err != nil {
		os.Remove(tempFile.Name())
		return "", err
	}

	screen.tempSubsFile = tempFile.Name()
	return screen.tempSubsFile, nil
}

func mobileTranscodeOptions(screen *FyneScreen) (*utils.TranscodeOptions, error) {
	subsPath := ""
	if screen.subsfile != nil && screen.castBurnSubtitles {
		var err error
		subsPath, err = copySubsToTempFile(screen)
		if err != nil {
			return nil, err
		}
	}

	opts := &utils.TranscodeOptions{
		FFmpegPath:   screen.ffmpegPath,
		SubsPath:     subsPath,
		SubtitleSize: utils.SubtitleSizeMedium,
		LogOutput:    screen.Debug,
	}
	if screen.castBurnSubtitles {
		opts.TorrentSource = mobileTorrentSubtitleSource(screen)
	}
	return opts, nil
}

// startChromecastMediaServer (re)starts the local HTTP server that serves the
// media to the Chromecast. mediaName is the media's own name, escaped here for
// the URL. It returns the served media URL together with a context that is
// cancelled once the server stops.
func startChromecastMediaServer(screen *FyneScreen, deviceAddr, mediaName string, tcOpts *utils.TranscodeOptions, media any, artworkAsset *metadata.ArtworkAsset) (string, context.Context, error) {
	whereToListen, err := utils.URLtoListenIPandPort(deviceAddr)
	if err != nil {
		return "", nil, err
	}

	// The handler is keyed on the path as the request will present it, which Go
	// has already unescaped by the time we look it up. Registering the escaped
	// form instead makes every name containing a space - most music files -
	// unreachable, and the device gets a 404 instead of the media. Parsing back
	// what we hand out is what keeps the two in step, as the DLNA path does.
	mediaURL := "http://" + whereToListen + "/" + utils.ConvertFilename(mediaName)
	parsedMediaURL, err := url.Parse(mediaURL)
	if err != nil {
		return "", nil, fmt.Errorf("chromecast media url: %w", err)
	}

	if screen.httpserver != nil {
		screen.httpserver.StopServer()
	}

	screen.httpserver = httphandlers.NewServer(whereToListen)
	registerGUIArtwork(screen.httpserver, artworkAsset)
	serverStoppedCTX, serverCTXStop := context.WithCancel(context.Background())
	screen.serverStopCTX = serverStoppedCTX
	screen.cancelServerStop = serverCTXStop

	screen.httpserver.AddHandler(parsedMediaURL.Path, nil, tcOpts, media)

	serverStarted := make(chan error)
	go func() {
		screen.httpserver.StartServing(serverStarted)
		serverCTXStop()
	}()

	if err := <-serverStarted; err != nil {
		return "", nil, err
	}

	return mediaURL, serverStoppedCTX, nil
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
	case ".srt", ".vtt", ".ass", ".ssa":
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
	if screen.stopPlaybackStartup() {
		return
	}
	stopActionInternal(screen, false)
}

// stopActionSync waits for DLNA teardown before a transcoded seek restart.
func stopActionSync(screen *FyneScreen) {
	if done := screen.cancelPlaybackStartup(); done != nil {
		<-done
	}
	stopActionInternal(screen, true)
}

func stopActionInternal(screen *FyneScreen, wait bool) {
	preserveSeek := screen.dlnaSeekRestart
	screen.persistDisplayedResumeProgress(true)
	screen.clearResumeSession()
	screen.nextChromecastActionID()
	chromecastClient := screen.chromecastClientForStop()
	screen.clearActiveDevice()

	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")

	// Clear casting media type immediately
	screen.SetMediaType("")

	// Clean up temp files
	removeTempFile(&screen.tempMediaFile)
	removeTempFile(&screen.tempSubsFile)

	// Keep the timeline on screen during a transcoded seek restart;
	// showDLNATranscodeTimeline repositions it once playback resumes.
	if !preserveSeek {
		fyne.Do(func() {
			screen.SlideBar.SetValue(0)
			screen.CurrentPos.Set("00:00:00")
			screen.EndPos.Set("00:00:00")
		})
		screen.dlnaSeekRestart = false
		screen.ffmpegSeek = 0
		screen.mediaDuration = 0
	}

	// Handle Chromecast stop
	if chromecastClient != nil {
		client := chromecastClient
		server := screen.httpserver
		client.Log().Debug("stopping Chromecast session", "Method", "StopAction")

		if screen.cancelServerStop != nil {
			screen.cancelServerStop()
			screen.cancelServerStop = nil
		}
		screen.serverStopCTX = nil
		screen.chromecastClient = nil
		screen.httpserver = nil

		teardown := func() {
			if client.IsConnected() {
				_ = client.Stop()
			}
			_ = client.Close(false)
			if server != nil {
				server.StopServer()
			}
		}
		if wait {
			teardown()
		} else {
			go teardown()
		}
		return
	}

	// Handle DLNA stop
	if screen.tvdata == nil || screen.tvdata.ControlURL == "" {
		return
	}

	// Capture references before clearing.
	tvdata := screen.tvdata
	server := screen.httpserver
	screen.tvdata = nil
	screen.httpserver = nil
	teardown := func() {
		if tvdata != nil && tvdata.ControlURL != "" {
			tvdata.SetContext(context.Background())
			_ = tvdata.SendtoTV("Stop")
		}
		if server != nil {
			server.StopServer()
		}
	}

	if wait {
		teardown()
		return
	}

	go teardown()
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
	selectedDevice := screen.selectedDevice
	go func() {
		// Handle Chromecast volume
		if selectedDevice.deviceType == devices.DeviceTypeChromecast {
			client, cleanup, err := selectedMobileChromecastControlClient(screen, selectedDevice)
			if err != nil {
				check(w, errors.New(lang.L("chromecast not connected")))
				return
			}
			defer cleanup()

			status, err := client.GetStatus()
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

			if err := client.SetVolume(newVolume); err != nil {
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
	ctx := screen.playbackStartupContext()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uri := screen.mediafile
	if uri != nil {
		if _, ok := mediasource.Lookup(uri.Path()); ok {
			return uri.Path(), nil
		}
	}

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
	stopClosing := context.AfterFunc(ctx, func() { _ = mediaReader.Close() })
	defer stopClosing()

	ext := filepath.Ext(screen.MediaText.Text)
	tempFile, err := createMobileCacheTemp("go2tv-*" + ext)
	if err != nil {
		return nil, fmt.Errorf("temp file create: %w", err)
	}

	if _, err := io.Copy(tempFile, mediaReader); err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("temp file copy: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		os.Remove(tempFile.Name())
		return nil, fmt.Errorf("temp file close: %w", err)
	}
	if err := ctx.Err(); err != nil {
		os.Remove(tempFile.Name())
		return nil, err
	}

	screen.tempMediaFile = tempFile.Name()
	return screen.tempMediaFile, nil
}

// dropDeadChromecastSession tears down a Chromecast session whose device is
// unreachable or idle: same cleanup as the status watcher's dead-connection
// path, for when the user action discovers it first.
func dropDeadChromecastSession(screen *FyneScreen, client *castprotocol.CastClient) {
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
	startAfreshPlayButton(screen)
}

func startAfreshPlayButton(screen *FyneScreen) {
	screen.persistDisplayedResumeProgress(true)
	screen.clearResumeSession()
	screen.nextChromecastActionID()

	screen.cancelPlayTimer()

	screen.clearActiveDevice()
	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")

	fyne.Do(func() {
		screen.SlideBar.SetValue(0)
		screen.CurrentPos.Set("00:00:00")
		screen.EndPos.Set("00:00:00")
	})
	screen.ffmpegSeek = 0
	screen.mediaDuration = 0
}

// chromecastPlayAction handles playback on Chromecast devices.
// Supports both local files (via internal HTTP server) and external URLs (direct).
func chromecastPlayAction(screen *FyneScreen, actionID uint64, startupCtx context.Context) {
	streamCtx, detachStream := playbackStreamContext(startupCtx)
	defer detachStream()
	if startupCtx.Err() != nil || !screen.isChromecastActionCurrent(actionID) {
		return
	}

	w := screen.Current
	sessionDevice := screen.selectedDevice

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
	screen.setActiveDevice(sessionDevice)

	// Reuse existing client if connected, otherwise create new one
	client := screen.chromecastClient
	if client == nil || !client.IsConnected() || !chromecastClientOwnsDevice(client, sessionDevice) {
		var err error
		client, err = connectChromecastForAction(screen, actionID, sessionDevice)
		if err != nil {
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}
		if !screen.installChromecastClientForAction(actionID, client) {
			_ = client.Close(false)
			return
		}
	}

	var mediaURL string
	var mediaType string
	var transcode bool
	var playbackStart int
	serverStoppedCTX := context.Background()
	subtitleHost := ""
	screen.clearResumeSession()
	screen.ffmpegSeek = 0
	screen.mediaDuration = 0
	screen.captureChromecastSubtitleSettings()

	if screen.ExternalMediaURL.Checked {
		screen.setCurrentArtwork(nil)
		mediaURL = screen.MediaText.Text

		mediaURLinfo, inferredMediaType, err := utils.StreamURLWithMime(streamCtx, mediaURL)
		if startupCtx.Err() != nil {
			if mediaURLinfo != nil {
				_ = mediaURLinfo.Close()
			}
			return
		}
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
			stream, err := utils.StreamURL(streamCtx, mediaURL)
			if startupCtx.Err() != nil {
				if stream != nil {
					_ = stream.Close()
				}
				return
			}
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

			servedURL, serverCTX, err := startChromecastMediaServer(screen, sessionDevice.addr, mediaURL, tcOpts, stream, artworkAsset)
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
		var err error
		mediaType, err = mobileMediaMIMEContext(startupCtx, screen.mediafile)
		if startupCtx.Err() != nil {
			return
		}
		if err != nil {
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}

		transcode = mediaTranscodeEnabled(screen, mediaType) && !isAudioMediaType(mediaType)
		storedResume := screen.prepareResumeSession(mediaType)
		resumeStart := computeChromecastResumeStart(0, storedResume)
		if transcode {
			screen.ffmpegSeek = resumeStart
		} else {
			playbackStart = resumeStart
		}

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
		if !torrentMediaSelected(screen) {
			artworkAsset = screen.resolveCurrentMobileGUIArtwork(screen.mediafile, mediaType, media)
		}

		var tcOpts *utils.TranscodeOptions
		if transcode {
			tcOpts, err = mobileTranscodeOptions(screen)
			if err != nil {
				check(w, err)
				startAfreshPlayButton(screen)
				return
			}
			tcOpts.SeekSeconds = screen.ffmpegSeek
			switch source := media.(type) {
			case string:
				if duration, err := utils.DurationForMediaSecondsContext(startupCtx, screen.ffmpegPath, source); err == nil {
					screen.mediaDuration = duration
				}
			case httphandlers.MediaReaderSeeker:
				if reader, openErr := source(); openErr == nil {
					screen.mediaDuration, _ = utils.DurationForMediaReaderSeconds(startupCtx, screen.ffmpegPath, reader)
					_ = reader.Close()
				}
			}
			mediaType = "video/mp4"
		}
		if startupCtx.Err() != nil {
			return
		}

		servedURL, serverCTX, err := startChromecastMediaServer(screen, sessionDevice.addr, screen.MediaText.Text, tcOpts, media, artworkAsset)
		if err != nil {
			check(w, err)
			startAfreshPlayButton(screen)
			return
		}

		serverStoppedCTX = serverCTX
		mediaURL = servedURL
	}

	if subtitleHost == "" {
		if parsed, parseErr := url.Parse(mediaURL); parseErr == nil {
			subtitleHost = parsed.Host
		}
	}
	offset := 0
	if transcode {
		offset = screen.ffmpegSeek
	}
	subtitleURL, err := registerMobileChromecastSubtitles(screen, subtitleHost, offset, transcode)
	if startupCtx.Err() != nil {
		return
	}
	if err != nil {
		check(w, err)
		startAfreshPlayButton(screen)
		return
	}

	// Use LIVE stream type for URL streams (DMR shows LIVE badge, but buffer unchanged)
	live := screen.ExternalMediaURL.Checked
	listenAddress := ""
	if parsedMediaURL, err := url.Parse(mediaURL); err == nil {
		listenAddress = parsedMediaURL.Host
	}
	torrentSubtitleURL := ""
	if screen.mediafile != nil {
		offset := 0
		if transcode {
			offset = screen.ffmpegSeek
		}
		torrentSubtitleURL = registerMobileTorrentSubtitles(screen, listenAddress, offset, transcode)
	}
	if startupCtx.Err() != nil {
		return
	}
	_, err = loadChromecastForAction(screen, actionID, sessionDevice, client, castprotocol.LoadRequest{
		MediaURL:           mediaURL,
		ContentType:        mediaType,
		Metadata:           guiMediaMetadata(chromecastMediaTitle(screen, mediaURL), listenAddress, artworkAsset),
		StartTime:          playbackStart,
		Duration:           screen.mediaDuration,
		SubtitleURL:        subtitleURL,
		TorrentSubtitleURL: torrentSubtitleURL,
		Live:               live,
	})
	if err != nil {
		if !screen.isChromecastActionCurrent(actionID) {
			return
		}
		check(w, fmt.Errorf("chromecast load: %w", err))
		startAfreshPlayButton(screen)
		return
	}
	if !screen.isChromecastActionCurrent(actionID) {
		return
	}
	screen.setActiveDevice(sessionDevice)
	screen.updateScreenState("Playing")
	setPlayPauseView("Pause", screen)
	go chromecastStatusWatcher(serverStoppedCTX, screen, actionID)
}

// chromecastTranscodedSeek restarts mobile transcoding at seekPos while
// retaining the existing Chromecast connection.
func chromecastTranscodedSeek(screen *FyneScreen, seekPos int) {
	releasePermit, permitted := screen.rendererPermit(true)
	if !permitted {
		return
	}

	actionID := screen.nextChromecastActionID()
	client := screen.activeChromecastPlaybackClient()
	if client == nil || !client.IsConnected() {
		releasePermit()
		return
	}

	sessionDevice := screen.getActiveDevice()
	if sessionDevice.addr == "" {
		sessionDevice = screen.selectedDevice
	}
	screen.ffmpegSeek = seekPos

	go func() {
		defer releasePermit()

		if screen.httpserver != nil {
			screen.httpserver.StopServer()
		}

		media, err := mobileMediaForTranscodedSeek(screen)
		if err != nil {
			check(screen.Current, err)
			return
		}

		tcOpts, err := mobileTranscodeOptions(screen)
		if err != nil {
			check(screen.Current, err)
			return
		}
		tcOpts.SeekSeconds = seekPos

		artworkAsset := screen.getCurrentArtwork()
		mediaURL, serverStoppedCTX, err := startChromecastMediaServer(
			screen,
			sessionDevice.addr,
			screen.MediaText.Text,
			tcOpts,
			media,
			artworkAsset,
		)
		if err != nil {
			check(screen.Current, err)
			return
		}

		listenAddress := ""
		if parsedMediaURL, parseErr := url.Parse(mediaURL); parseErr == nil {
			listenAddress = parsedMediaURL.Host
		}
		subtitleURL, err := registerMobileChromecastSubtitles(screen, listenAddress, seekPos, true)
		if err != nil {
			check(screen.Current, err)
			return
		}
		torrentSubtitleURL := ""
		if screen.mediafile != nil {
			torrentSubtitleURL = registerMobileTorrentSubtitles(screen, listenAddress, seekPos, true)
		}
		if err := client.LoadMediaOnExisting(castprotocol.LoadRequest{
			MediaURL:           mediaURL,
			ContentType:        "video/mp4",
			SubtitleURL:        subtitleURL,
			TorrentSubtitleURL: torrentSubtitleURL,
			Metadata:           guiMediaMetadata(chromecastMediaTitle(screen, mediaURL), listenAddress, artworkAsset),
			Duration:           screen.mediaDuration,
		}); err != nil {
			check(screen.Current, fmt.Errorf("chromecast seek load: %w", err))
			return
		}

		go chromecastStatusWatcher(serverStoppedCTX, screen, actionID)
	}()
}

func mobileMediaForTranscodedSeek(screen *FyneScreen) (any, error) {
	if screen.tempMediaFile != "" {
		if _, err := os.Stat(screen.tempMediaFile); err == nil {
			return screen.tempMediaFile, nil
		}
	}
	if screen.mediafile == nil {
		return nil, errors.New("mobile media unavailable")
	}
	return seekableMediaForCasting(screen)
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
	stallLastTime := -1.0
	stallDuration := 0.0
	stallTicks := 0

	// Consecutive GetStatus failures, see the dead-connection handling.
	statusErrs := 0
	startupIdleTicks := 0

	// Last mute state pushed to the UI, nil until the first sample.
	var lastMuted *bool

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

			// Mute state rides along with the status poll (checkMutefunc
			// skips Chromecast to avoid a second GetStatus loop).
			if muted := status.Muted; chromecastClientOwnsDevice(client, screen.selectedDevice) && (lastMuted == nil || *lastMuted != muted) {
				lastMuted = &muted
				if muted {
					setMuteUnmuteView("Unmute", screen)
				} else {
					setMuteUnmuteView("Mute", screen)
				}
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

			// For transcoded streams, use the source duration and seek offset.
			currentTime, duration := chromecastProgressTimeline(
				screen.mediaDuration,
				screen.ffmpegSeek,
				status.Duration,
				status.CurrentTime,
			)

			// Chromecast reports 0 duration/time during buffering.
			sampleValid := status.PlayerState != "BUFFERING" && duration > 0
			if sampleValid && mediaStarted && !screen.sliderActive {
				// Display only: ffprobe durations can undershoot, letting the
				// position pass the reported end. The stall net keeps the raw
				// values so real progress past the end never reads as a wedge.
				shownTime := min(currentTime, duration)
				progress := (shownTime / duration) * screen.SlideBar.Max
				fyne.Do(func() {
					screen.SlideBar.SetValue(progress)
					screen.CurrentPos.Set(utils.SecondsToClockTime(int(shownTime)))
					screen.EndPos.Set(utils.SecondsToClockTime(int(duration)))
				})
				screen.persistResumeProgress(int(shownTime), duration, false)
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
			if sampleValid && currentTime != stallLastTime {
				// Progress (or a seek): remember the latest good sample.
				stallLastTime = currentTime
				stallDuration = duration
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
