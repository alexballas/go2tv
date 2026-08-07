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

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/theme"
	xfilepicker "github.com/alexballas/xfilepicker/dialog"
	"github.com/pkg/errors"
	"github.com/skratchdot/open-golang/open"
	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/rtmp"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
	"go2tv.app/screencast/hls"
)

const (
	filePickerFillSize      = 10000
	imagePreviewMinWidth    = 160
	imagePreviewMinHeight   = 120
	imagePreviewStartWidth  = 800
	imagePreviewStartHeight = 600
)

func armChromecastImageAutoSkipAfterReady(screen *FyneScreen, client *castprotocol.CastClient, actionID uint64, mediaType, mediaPath string) {
	if !strings.HasPrefix(mediaType, "image/") {
		return
	}

	go func() {
		deadline := time.Now().Add(8 * time.Second)
		var readySince time.Time

		for {
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}
			if client == nil || !client.IsConnected() {
				return
			}

			status, err := client.GetStatus()
			if err == nil {
				if chromecastImageStatusReady(status) && status.PlayerState != "BUFFERING" {
					screen.configureImageAutoSkipTimer(mediaType, mediaPath)
					return
				}

				// Some Chromecast devices keep reporting BUFFERING for static images.
				// If metadata is ready for a bit, arm anyway.
				if chromecastImageStatusReady(status) && status.PlayerState == "BUFFERING" {
					if readySince.IsZero() {
						readySince = time.Now()
					}
					if time.Since(readySince) >= 2*time.Second {
						screen.configureImageAutoSkipTimer(mediaType, mediaPath)
						return
					}
				} else {
					readySince = time.Time{}
				}
			}

			if time.Now().After(deadline) {
				// Fallback: keep feature working even if device doesn't expose ContentType reliably.
				screen.configureImageAutoSkipTimer(mediaType, mediaPath)
				return
			}

			time.Sleep(250 * time.Millisecond)
		}
	}()
}

func chromecastImageStatusReady(status *castprotocol.CastStatus) bool {
	if status == nil {
		return false
	}
	if strings.HasPrefix(status.ContentType, "image/") {
		return true
	}
	if status.MediaTitle != "" {
		return true
	}

	return status.PlayerState == "PLAYING" || status.PlayerState == "PAUSED"
}

func chromecastMediaTitle(screen *FyneScreen, fallback string) string {
	if screen == nil {
		return fallback
	}
	if screen.Screencast {
		return "Screencast"
	}
	if title := strings.TrimSpace(screen.mediafile); title != "" {
		return title
	}
	if screen.MediaText != nil {
		if title := strings.TrimSpace(screen.MediaText.Text); title != "" {
			return title
		}
	}
	return fallback
}

func selectedChromecastControlClient(screen *FyneScreen) (*castprotocol.CastClient, func(), error) {
	if screen.selectedDeviceType != devices.DeviceTypeChromecast || screen.selectedDevice.addr == "" {
		return nil, nil, errors.New(lang.L("chromecast not connected"))
	}

	if client := screen.reusableChromecastClientForSelectedDevice(); client != nil {
		return client, func() {}, nil
	}

	client, err := castprotocol.NewCastClient(screen.selectedDevice.addr)
	if err != nil {
		return nil, nil, fmt.Errorf("chromecast init: %w", err)
	}
	client.LogOutput = screen.Debug

	if err := client.Connect(); err != nil {
		return nil, nil, fmt.Errorf("chromecast connect: %w", err)
	}

	return client, func() { _ = client.Close(false) }, nil
}

func muteAction(screen *FyneScreen) {
	if screen.isMuted() {
		unmuteAction(screen)
		return
	}

	releasePermit, permitted := screen.rendererPermit(true)
	if !permitted {
		return
	}

	// Handle Chromecast mute for selected device.
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		go func() {
			defer releasePermit()
			client, cleanup, err := selectedChromecastControlClient(screen)
			if err != nil {
				check(screen, errors.New(lang.L("chromecast not connected")))
				return
			}
			defer cleanup()

			if err := client.SetMuted(true); err != nil {
				check(screen, errors.New(lang.L("could not send mute action")))
				return
			}
			setMuteUnmuteView(true, screen)
		}()
		return
	}

	// Handle DLNA mute
	if screen.renderingControlURL == "" {
		releasePermit()
		check(screen, errors.New(lang.L("please select a device")))
		return
	}

	go func() {
		defer releasePermit()
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

		setMuteUnmuteView(true, screen)
	}()
}

func unmuteAction(screen *FyneScreen) {
	releasePermit, permitted := screen.rendererPermit(true)
	if !permitted {
		return
	}

	// Handle Chromecast unmute for selected device.
	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		go func() {
			defer releasePermit()
			client, cleanup, err := selectedChromecastControlClient(screen)
			if err != nil {
				check(screen, errors.New(lang.L("chromecast not connected")))
				return
			}
			defer cleanup()

			if err := client.SetMuted(false); err != nil {
				check(screen, errors.New(lang.L("could not send mute action")))
				return
			}
			setMuteUnmuteView(false, screen)
		}()
		return
	}

	// Handle DLNA unmute
	if screen.renderingControlURL == "" {
		releasePermit()
		check(screen, errors.New(lang.L("please select a device")))
		return
	}

	go func() {
		defer releasePermit()
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

		setMuteUnmuteView(false, screen)
	}()
}

func selectMediaFile(screen *FyneScreen, f fyne.URI) {
	if err := selectMediaPaths(screen, []string{f.Path()}); err != nil {
		check(screen, err)
	}
}

func selectMediaPaths(screen *FyneScreen, paths []string) error {
	items := screen.buildQueueItems(paths)
	if len(items) == 0 {
		return errors.New(lang.L("please select a media file"))
	}

	screen.replaceSessionQueue(items, 0)

	return setCurrentMediaPath(screen, items[0].Path())
}

func appendMediaPaths(screen *FyneScreen, paths []string) error {
	itemsToAdd := screen.buildQueueItems(paths)
	if len(itemsToAdd) == 0 {
		return errors.New(lang.L("please select a media file"))
	}

	queue, _ := screen.queueSnapshot()
	combined := make([]QueueItem, 0, len(itemsToAdd)+1)
	seen := make(map[string]struct{}, len(itemsToAdd)+1)
	addItem := func(item QueueItem) {
		if _, ok := seen[item.Path()]; ok {
			return
		}
		seen[item.Path()] = struct{}{}
		combined = append(combined, item)
	}

	currentIndex := 0
	if queue != nil && queue.Len() > 0 {
		for _, item := range queue.Items() {
			addItem(item)
		}
		currentIndex = queue.CurrentIndex()
	} else if screen.mediafile != "" && (screen.ExternalMediaURL == nil || !screen.ExternalMediaURL.Checked) {
		if currentItem, ok := screen.newQueueItem(screen.mediafile); ok {
			addItem(currentItem)
		}
	}

	for _, item := range itemsToAdd {
		addItem(item)
	}

	if len(combined) == 0 {
		return errors.New(lang.L("please select a media file"))
	}

	screen.replaceSessionQueue(combined, currentIndex)
	if screen.mediafile == "" {
		if err := setCurrentMediaPath(screen, combined[0].Path()); err != nil {
			return err
		}
		screen.scrollQueueListToBottom()
		return nil
	}

	screen.scrollQueueListToBottom()
	return nil
}

func setCurrentMediaPath(screen *FyneScreen, mediaPath string) error {
	absMediaFile, err := filepath.Abs(mediaPath)
	if err != nil {
		return err
	}

	if screen.ExternalMediaURL != nil && screen.ExternalMediaURL.Checked {
		fyne.DoAndWait(func() {
			screen.ExternalMediaURL.SetChecked(false)
		})
	}

	screen.mediafile = absMediaFile
	screen.currentmfolder = filepath.Dir(absMediaFile)
	screen.syncQueueCurrentWithMedia(absMediaFile)
	screen.setCurrentArtworkTarget(guiArtworkIdentity(absMediaFile))
	if screen.mediaKindForPath(absMediaFile) == "audio" {
		go screen.resolveCurrentGUIArtwork(absMediaFile, "audio/unknown", true)
	}

	fyne.Do(func() {
		if screen.SelectInternalSubs != nil {
			screen.SelectInternalSubs.ClearSelected()
		}
		if screen.MediaText != nil {
			screen.MediaText.SetText(filepath.Base(absMediaFile))
		}
	})

	if !screen.CustomSubsCheck.Checked {
		autoSelectNextSubs(absMediaFile, screen)
	}

	updateInternalSubsDropdown(screen, absMediaFile)

	if screen.selectedDeviceType == devices.DeviceTypeChromecast {
		screen.checkChromecastCompatibility()
	}

	screen.refreshQueueStateUI()
	setPlayPauseView("", screen)
	return nil
}

func clearCurrentMediaSelection(screen *FyneScreen) {
	if screen.MediaText != nil {
		screen.MediaText.SetText("")
	}
	screen.mediafile = ""
	screen.setCurrentArtwork(nil)
	screen.clearQueueCurrent()
	setInternalSubsDropdownNoSubs(screen)
	screen.refreshQueueStateUI()
	setPlayPauseView("", screen)
}

func restoreMediaInputState(screen *FyneScreen, mediaPath, mediaText string) {
	if screen.ExternalMediaURL != nil && screen.ExternalMediaURL.Checked {
		if screen.MediaText != nil {
			screen.MediaText.SetText(mediaText)
		}
		screen.mediafile = mediaPath
		setInternalSubsDropdownNoSubs(screen)
		screen.refreshQueueStateUI()
		setPlayPauseView("", screen)
		return
	}

	if mediaPath == "" {
		clearCurrentMediaSelection(screen)
		return
	}

	if err := setCurrentMediaPath(screen, mediaPath); err != nil {
		check(screen, err)
	}
}

func openMediaPicker(screen *FyneScreen, onPaths func(*FyneScreen, []string) error) {
	openMediaPickerForWindow(screen, screen.Current, onPaths, nil)
}

func openMediaPickerForWindow(screen *FyneScreen, w fyne.Window, onPaths func(*FyneScreen, []string) error, onDone func()) {
	xfilepicker.SetFFmpegPath(screen.ffmpegPath)
	var resumeHotkeys func()
	fd := xfilepicker.NewFileOpen(func(readers []fyne.URIReadCloser, err error) {
		if resumeHotkeys != nil {
			defer resumeHotkeys()
		}
		if onDone != nil {
			defer onDone()
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

		paths := make([]string, 0, len(readers))
		for _, reader := range readers {
			paths = append(paths, reader.URI().Path())
		}

		check(screen, onPaths(screen, paths))
	}, w, true)

	if f, ok := fd.(xfilepicker.FilePicker); ok {
		f.SetFilter(storage.NewExtensionFileFilter(screen.mediaFormats))
	}

	if screen.currentmfolder != "" {
		mfileURI := storage.NewFileURI(screen.currentmfolder)
		mfileLister, err := storage.ListerForURI(mfileURI)

		if err != nil || mfileLister == nil {
			check(screen, err)
			screen.currentmfolder = ""
		} else if f, ok := fd.(xfilepicker.FilePicker); ok {
			f.SetLocation(mfileLister)
		}
	}

	resumeHotkeys = suspendHotkeys(screen)
	fd.Show()
	fd.Resize(fyne.NewSize(filePickerFillSize, filePickerFillSize))
}

func setInternalSubsDropdownNoSubs(screen *FyneScreen) {
	screen.SelectInternalSubs.Options = []string{}
	screen.SelectInternalSubs.PlaceHolder = lang.L("No Embedded Subs")
	screen.SelectInternalSubs.ClearSelected()
	screen.SelectInternalSubs.Disable()
}

func setInternalSubsDropdownWithSubs(screen *FyneScreen, subs []string) {
	screen.SelectInternalSubs.Options = subs
	screen.SelectInternalSubs.PlaceHolder = lang.L("Embedded Subs")
	screen.SelectInternalSubs.ClearSelected()
	screen.SelectInternalSubs.Enable()
}

func getInternalSubsDropdownOptions(screen *FyneScreen, mediaFile string) ([]string, bool) {
	subs, err := utils.GetSubs(screen.ffmpegPath, mediaFile)
	if err != nil {
		return nil, false
	}

	return subs, true
}

// updateInternalSubsDropdown refreshes the embedded subtitles dropdown
// for the given media file. Should be called when media file changes
// (e.g., via Next button or auto-play).
func updateInternalSubsDropdown(screen *FyneScreen, mediaFile string) {
	subs, ok := getInternalSubsDropdownOptions(screen, mediaFile)

	fyne.Do(func() {
		if !ok {
			setInternalSubsDropdownNoSubs(screen)
			return
		}
		setInternalSubsDropdownWithSubs(screen, subs)
	})
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
	openMediaPicker(screen, selectMediaPaths)
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
		f.SetFilter(storage.NewExtensionFileFilter(mediamodel.SRTExtensions()))
	}

	if screen.currentmfolder != "" {
		mfileURI := storage.NewFileURI(screen.currentmfolder)
		mfileLister, err := storage.ListerForURI(mfileURI)
		if err != nil || mfileLister == nil {
			check(screen, err)
			screen.currentmfolder = ""
		} else if f, ok := fd.(xfilepicker.FilePicker); ok {
			f.SetLocation(mfileLister)
		}
	}
	resumeHotkeys = suspendHotkeys(screen)
	fd.Show()
	fd.Resize(fyne.NewSize(filePickerFillSize, filePickerFillSize))
}

type playbackTarget struct {
	device               devType
	controlURL           string
	eventURL             string
	renderingControlURL  string
	connectionManagerURL string
}

func selectedPlaybackTarget(screen *FyneScreen) playbackTarget {
	return playbackTarget{
		device:               screen.selectedDevice,
		controlURL:           screen.controlURL,
		eventURL:             screen.eventURL,
		renderingControlURL:  screen.renderingControlURL,
		connectionManagerURL: screen.connectionManagerURL,
	}
}

func activeSessionPlaybackTarget(screen *FyneScreen) (playbackTarget, bool) {
	activeDevice := screen.getActiveDevice()
	if activeDevice.addr == "" {
		return playbackTarget{}, false
	}

	target := playbackTarget{device: activeDevice}
	if activeDevice.deviceType == devices.DeviceTypeDLNA && screen.tvdata != nil {
		target.controlURL = screen.tvdata.ControlURL
		target.eventURL = screen.tvdata.EventURL
		target.renderingControlURL = screen.tvdata.RenderingControlURL
		target.connectionManagerURL = screen.tvdata.ConnectionManagerURL
	}

	return target, true
}

func traversalPlaybackTarget(screen *FyneScreen) playbackTarget {
	switch screen.getScreenState() {
	case "Playing", "Paused":
		if target, ok := activeSessionPlaybackTarget(screen); ok {
			return target
		}
	}

	return selectedPlaybackTarget(screen)
}

func autoPlayPlaybackTarget(screen *FyneScreen) playbackTarget {
	if target, ok := activeSessionPlaybackTarget(screen); ok {
		return target
	}

	return selectedPlaybackTarget(screen)
}

func playAction(screen *FyneScreen) {
	playActionOnTarget(screen, selectedPlaybackTarget(screen))
}

func playActionOnTarget(screen *FyneScreen, target playbackTarget) {
	releasePermit, permitted := screen.rendererPermit(true)
	if !permitted {
		return
	}
	permitHandedOff := false
	defer func() {
		if !permitHandedOff {
			releasePermit()
		}
	}()

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

	// Active Chromecast session: control the session owner, not a warm reusable client.
	if client := screen.activeChromecastPlaybackClient(); client != nil && isActivePlayback {
		// Network round trips, so off the tap thread; hand the permit over.
		permitHandedOff = true
		go func() {
			defer releasePermit()
			// The screen state can be stale (a session that died or finished
			// while unobserved), so toggle on the device's live state instead
			// of writing a command into a possibly dead socket.
			status, err := client.GetStatus()
			if err != nil {
				// Dead session: drop the client so follow-up actions reconnect.
				if screen.chromecastClient == client {
					screen.chromecastClient = nil
				}
				go client.Close(false)
				startAfreshPlayButton(screen)
				return
			}
			switch status.PlayerState {
			case "PLAYING", "BUFFERING":
				if err := client.Pause(); err != nil {
					check(screen, err)
					return
				}
				setPlayPauseView("Play", screen)
				screen.updateScreenState("Paused")
			case "PAUSED":
				if err := client.Play(); err != nil {
					check(screen, err)
					return
				}
				setPlayPauseView("Pause", screen)
				screen.updateScreenState("Playing")
			default:
				// IDLE: playback ended while the UI still showed a session.
				if screen.chromecastClient == client {
					screen.chromecastClient = nil
				}
				go client.Close(false)
				startAfreshPlayButton(screen)
			}
		}()
		return
	}
	if !screen.Screencast && screen.mediafile == "" && screen.MediaText.Text == "" {
		check(screen, errors.New(lang.L("please select a media file or enter a media URL")))
		startAfreshPlayButton(screen)
		return
	}

	if target.device.addr == "" {
		check(screen, errors.New(lang.L("please select a device")))
		startAfreshPlayButton(screen)
		return
	}

	if screen.Screencast && target.device.deviceType != devices.DeviceTypeChromecast &&
		target.device.deviceType != devices.DeviceTypeDLNA {
		check(screen, errors.New(lang.L("screencast currently supports Chromecast and DLNA only")))
		startAfreshPlayButton(screen)
		return
	}

	// Branch based on device type - MUST be first, before any DLNA-specific logic
	// Chromecast has its own status watcher, doesn't need the DLNA timeout mechanism
	if target.device.deviceType == devices.DeviceTypeChromecast {
		actionID := screen.nextChromecastActionID()
		permitHandedOff = true
		go func() {
			defer releasePermit()
			chromecastPlayAction(screen, actionID, target.device)
		}()
		return
	}

	// DLNA timeout mechanism - re-enable play button if no response after 3 seconds
	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}
	sessionDevice := target.device

	ctx, cancelEnablePlay := context.WithTimeout(context.Background(), 3*time.Second)
	screen.cancelEnablePlay = cancelEnablePlay

	permitHandedOff = true
	go func() {
		defer releasePermit()
		// DLNA desktop mirroring (Cast Desktop) uses its own pipeline.
		if screen.Screencast {
			dlnaScreencastPlayAction(screen, target)
			return
		}
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

		if target.controlURL == "" {
			check(screen, errors.New(lang.L("please select a device")))
			startAfreshPlayButton(screen)
			return
		}

		whereToListen, err := utils.URLtoListenIPandPort(target.controlURL)
		check(screen, err)
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		var mediaType string
		var isSeek bool
		var directResumeSeek int
		transcodeEnabled := screen.Transcode
		existingSeek := 0
		if screen.dlnaSeekRestart {
			existingSeek = screen.ffmpegSeek
		}
		screen.dlnaSeekRestart = false
		screen.ffmpegSeek = existingSeek
		screen.clearResumeSession()

		if !screen.ExternalMediaURL.Checked {
			if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
				screen.setCurrentArtwork(nil)
				mediaType = "application/vnd.apple.mpegurl"
				screen.SetMediaType(mediaType)
			} else {
				mediaType, err = utils.GetMimeDetailsFromPath(screen.mediafile)
				check(screen, err)
				if err != nil {
					startAfreshPlayButton(screen)
					return
				}
				screen.resolveCurrentGUIArtwork(screen.mediafile, mediaType, true)

				// Set casting media type
				screen.SetMediaType(mediaType)

				if !transcodeEnabled {
					isSeek = true
				}

				storedResume := screen.prepareResumeSession(mediaType)
				screen.ffmpegSeek, directResumeSeek = computeResumeStart(existingSeek, storedResume, transcodeEnabled)
			}
		}

		callbackPath, err := utils.RandomString()
		if err != nil {
			startAfreshPlayButton(screen)
			return
		}

		mediaFile = screen.mediafile

		if screen.ExternalMediaURL.Checked {
			screen.setCurrentArtwork(nil)
			// We need to define the screen.mediafile
			// as this is the core item in our structure
			// that defines that something is being streamed.
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
				mediaURL, inferredMediaType, err := utils.StreamURLWithMime(context.Background(), screen.MediaText.Text)
				check(screen, err)
				if err != nil {
					startAfreshPlayButton(screen)
					return
				}

				mediaType = inferredMediaType

				// Set casting media type
				screen.SetMediaType(mediaType)
				if utils.IsHLSStream(screen.MediaText.Text, mediaType) {
					transcodeEnabled = false
					fyne.Do(func() {
						if screen.TranscodeCheckBox != nil && screen.TranscodeCheckBox.Checked {
							screen.TranscodeCheckBox.SetChecked(false)
						}
					})
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
		mediaDuration := 0.0
		if transcodeEnabled {
			if duration, probeErr := utils.DurationForMediaSeconds(screen.ffmpegPath, screen.mediafile); probeErr == nil && duration > 0 {
				mediaDuration = duration
			}
		}
		screen.mediaDuration = mediaDuration
		if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
			screen.tvdata = &soapcalls.TVPayload{
				ControlURL:                  target.controlURL,
				EventURL:                    target.eventURL,
				RenderingControlURL:         target.renderingControlURL,
				ConnectionManagerURL:        target.connectionManagerURL,
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
				ControlURL:                  target.controlURL,
				EventURL:                    target.eventURL,
				RenderingControlURL:         target.renderingControlURL,
				ConnectionManagerURL:        target.connectionManagerURL,
				MediaURL:                    "http://" + whereToListen + "/" + utils.ConvertFilename(screen.mediafile),
				SubtitlesURL:                "http://" + whereToListen + "/" + utils.ConvertFilename(screen.subsfile),
				CallbackURL:                 "http://" + whereToListen + "/" + callbackPath,
				MediaType:                   mediaType,
				MediaPath:                   screen.mediafile,
				CurrentTimers:               make(map[string]*time.Timer),
				MediaRenderersStates:        make(map[string]*soapcalls.States),
				InitialMediaRenderersStates: make(map[string]bool),
				Transcode:                   transcodeEnabled,
				Seekable:                    isSeek,
				MediaDuration:               mediaDuration,
				LogOutput:                   screen.Debug,
				FFmpegPath:                  screen.ffmpegPath,
				FFmpegSeek:                  screen.ffmpegSeek,
				FFmpegSubsPath:              screen.subsfile,
			}
		}
		showDLNATranscodeTimeline(screen, screen.tvdata)
		if screen.httpserver != nil {
			screen.httpserver.StopServer()
		}
		screen.resetQueuedArtworkState()

		screen.httpserver = httphandlers.NewServer(whereToListen)
		artworkAsset := screen.getCurrentArtwork()
		registerGUIArtwork(screen.httpserver, artworkAsset)
		screen.tvdata.Metadata = guiMediaMetadata("", whereToListen, artworkAsset)
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
		if directResumeSeek > 0 && !screen.tvdata.Transcode {
			screen.applyInitialDLNAResume(screen.tvdata, directResumeSeek)
		}
		if strings.HasPrefix(mediaType, "image/") {
			screen.updateScreenState("Playing")
			setPlayPauseView("Pause", screen)
		}
		screen.configureImageAutoSkipTimer(mediaType, screen.mediafile)

		gaplessOption := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")
		if screen.NextMediaCheck.Checked && gaplessOption == "Enabled" {
			newTVPayload, err := queueNext(screen, false)
			if err != nil {
				check(screen, err)
				return
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

		if screen.tvdata == nil {
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
			screen.refreshImageAutoSkipTimer()
		case "PAUSED_PLAYBACK":
			setPlayPauseView("Play", screen)
			screen.updateScreenState("Paused")
		}
	}()
}

func startChromecastScreencast(screen *FyneScreen, device devType) (string, string, context.Context, error) {
	if err := screen.validateFFmpeg(); err != nil {
		return "", "", nil, errors.New(lang.L("ffmpeg is required for screencast"))
	}

	if device.isAudioOnly {
		return "", "", nil, errors.New(lang.L("screencast is not supported by audio-only device"))
	}

	stopScreencastSession(screen)

	whereToListen, err := utils.URLtoListenIPandPort(device.addr)
	if err != nil {
		return "", "", nil, err
	}

	if screen.httpserver != nil {
		screen.httpserver.StopServer()
	}

	session, err := hls.Start(&hls.Options{
		FFmpegPath:         screen.ffmpegPath,
		IncludeAudio:       hls.BoolEnv("GO2TV_SCREENCAST_AUDIO", true),
		HLSDeleteThreshold: hls.IntEnvClamped("GO2TV_SCREENCAST_HLS_DELETE_THRESHOLD", 24, 1, 120),
		TempDirPrefix:      "go2tv-screencast-",
		LogOutput:          screen.Debug,
		DebugCommand:       hls.BoolEnv("GO2TV_FFMPEG_DEBUG", false),
	})
	if err != nil {
		return "", "", nil, err
	}

	screen.screencastMu.Lock()
	screen.screencastSession = session
	screen.screencastMu.Unlock()

	screen.httpserver = httphandlers.NewServer(whereToListen)
	serverStarted := make(chan error)
	serverStoppedCTX, serverCTXStop := context.WithCancel(context.Background())
	screen.serverStopCTX = serverStoppedCTX
	screen.cancelServerStop = serverCTXStop
	screen.httpserver.AddHLSHandler("/live/", session.Dir())

	go func() {
		screen.httpserver.StartServing(serverStarted)
		serverCTXStop()
	}()

	if err := <-serverStarted; err != nil {
		stopScreencastSession(screen)
		return "", "", nil, err
	}

	go monitorScreencastFFmpeg(screen, session)

	screen.ffmpegSeek = 0
	screen.mediaDuration = 0
	screen.SetMediaType("application/vnd.apple.mpegurl")

	return "http://" + whereToListen + "/live/playlist.m3u8", "application/vnd.apple.mpegurl", serverStoppedCTX, nil
}

// dlnaScreencastPlayAction starts desktop mirroring on DLNA devices. It
// captures the screen, transcodes it to a live MPEG-TS stream with ffmpeg and
// streams it to the renderer through the DLNA SetAVTransportURI/Play flow.
func dlnaScreencastPlayAction(screen *FyneScreen, target playbackTarget) {
	if err := screen.validateFFmpeg(); err != nil {
		check(screen, errors.New(lang.L("ffmpeg is required for screencast")))
		startAfreshPlayButton(screen)
		return
	}

	if target.device.isAudioOnly {
		check(screen, errors.New(lang.L("screencast is not supported by audio-only device")))
		startAfreshPlayButton(screen)
		return
	}

	if target.controlURL == "" {
		check(screen, errors.New(lang.L("please select a device")))
		startAfreshPlayButton(screen)
		return
	}

	stopScreencastSession(screen)

	whereToListen, err := utils.URLtoListenIPandPort(target.device.addr)
	if err != nil {
		check(screen, err)
		startAfreshPlayButton(screen)
		return
	}

	session, err := startDLNAScreencast(screen.ffmpegPath, screen.Debug)
	if err != nil {
		check(screen, fmt.Errorf("failed to start dlna screencast: %w", err))
		startAfreshPlayButton(screen)
		return
	}

	screen.screencastMu.Lock()
	screen.screencastSession = session
	screen.screencastMu.Unlock()

	go monitorScreencastFFmpeg(screen, session)

	if screen.httpserver != nil {
		screen.httpserver.StopServer()
	}

	callbackPath, err := utils.RandomString()
	if err != nil {
		stopScreencastSession(screen)
		check(screen, err)
		startAfreshPlayButton(screen)
		return
	}

	sessionDevice := target.device
	tvdata := &soapcalls.TVPayload{
		ControlURL:                  target.controlURL,
		EventURL:                    target.eventURL,
		RenderingControlURL:         target.renderingControlURL,
		ConnectionManagerURL:        target.connectionManagerURL,
		MediaURL:                    "http://" + whereToListen + "/screencast",
		SubtitlesURL:                "http://" + whereToListen + "/.",
		CallbackURL:                 "http://" + whereToListen + "/" + callbackPath,
		MediaType:                   dlnaScreencastMediaType,
		MediaPath:                   "screencast",
		Metadata:                    metadata.Media{Title: lang.L("Cast Desktop Live Stream")},
		CurrentTimers:               make(map[string]*time.Timer),
		MediaRenderersStates:        make(map[string]*soapcalls.States),
		InitialMediaRenderersStates: make(map[string]bool),
		Transcode:                   false,
		Seekable:                    false,
		LogOutput:                   screen.Debug,
		FFmpegPath:                  screen.ffmpegPath,
	}
	screen.tvdata = tvdata

	screen.httpserver = httphandlers.NewServer(whereToListen)
	serverStarted := make(chan error)
	serverStoppedCTX, serverCTXStop := context.WithCancel(context.Background())
	screen.serverStopCTX = serverStoppedCTX
	screen.cancelServerStop = serverCTXStop
	liveStream := &screencastStream{stream: session.Stream()}
	go func() {
		screen.httpserver.StartServer(serverStarted, httphandlers.LiveStream(liveStream.acquire), nil, tvdata, screen)
		serverCTXStop()
	}()

	if err := <-serverStarted; err != nil {
		stopScreencastSession(screen)
		check(screen, err)
		startAfreshPlayButton(screen)
		return
	}

	if err := tvdata.SendtoTV("Play1"); err != nil {
		check(screen, err)
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
	screen.ffmpegSeek = 0
	screen.mediaDuration = 0
	screen.SetMediaType(dlnaScreencastMediaType)
	screen.updateScreenState("Playing")
	setPlayPauseView("Pause", screen)
}

func monitorScreencastFFmpeg(screen *FyneScreen, session screencastSession) {
	err, ok := <-session.Done()
	if !ok {
		return
	}

	screen.screencastMu.Lock()
	stillActive := screen.screencastSession == session
	screen.screencastMu.Unlock()
	if !stillActive {
		return
	}

	if err != nil {
		check(screen, fmt.Errorf("screencast ffmpeg stopped: %w: %s", err, session.StderrTail(300)))
	} else {
		check(screen, errors.New(lang.L("screencast stream stopped unexpectedly")))
	}

	fyne.Do(func() {
		if screen.ScreencastCheckBox != nil && screen.ScreencastCheckBox.Checked {
			screen.ScreencastCheckBox.SetChecked(false)
		}
	})
}

func stopScreencastSession(screen *FyneScreen) {
	screen.screencastMu.Lock()
	session := screen.screencastSession
	screen.screencastSession = nil
	screen.screencastMu.Unlock()

	if session != nil {
		_ = session.Close()
	}
}

// chromecastPlayAction handles playback on Chromecast devices.
// Supports both local files (via internal HTTP server) and external URLs (direct).
func chromecastPlayAction(screen *FyneScreen, actionID uint64, sessionDevice devType) {
	if !screen.isChromecastActionCurrent(actionID) {
		return
	}

	// Handle pause/resume if already playing on the active Chromecast session.
	if client := screen.activeChromecastPlaybackClient(); client != nil {
		currentState := screen.getScreenState()
		if currentState == "Paused" {
			if err := client.Play(); err != nil {
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
			if err := client.Pause(); err != nil {
				check(screen, err)
				return
			}
			// Update UI to show Play button (pause succeeded)
			setPlayPauseView("Play", screen)
			screen.updateScreenState("Paused")
			return
		}
	}
	var artworkAsset *metadata.ArtworkAsset

	screen.setActiveDevice(sessionDevice)

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
	screen.clearResumeSession()

	transcode := screen.Transcode
	ffmpegSeek := screen.ffmpegSeek

	// Handle internal (embedded) subtitles extraction
	if !screen.Screencast && screen.SelectInternalSubs.Selected != "" {
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

	// Reuse an existing client only for the target Chromecast device.
	client := screen.reusableChromecastClientForDevice(sessionDevice)
	if client == nil {
		staleClient := screen.chromecastClient
		if staleClient != nil && staleClient.IsConnected() && !chromecastClientOwnsDevice(staleClient, sessionDevice) {
			screen.chromecastClient = nil
			go staleClient.Close(false)
		}

		var err error
		client, err = castprotocol.NewCastClient(sessionDevice.addr)
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

	if screen.Screencast {
		screen.setCurrentArtwork(nil)
		mediaURL, mediaType, serverStoppedCTX, err := startChromecastScreencast(screen, sessionDevice)
		if err != nil {
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}
			fyne.Do(func() {
				if screen.ScreencastCheckBox != nil && screen.ScreencastCheckBox.Checked {
					screen.ScreencastCheckBox.SetChecked(false)
				}
			})
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}

		go func() {
			if err := client.Load(mediaURL, mediaType, chromecastMediaTitle(screen, mediaURL), 0, 0, "", true); err != nil {
				if !screen.isChromecastActionCurrent(actionID) {
					return
				}
				if screen.httpserver != nil {
					screen.httpserver.StopServer()
				}
				check(screen, fmt.Errorf("chromecast load: %w", err))
				startAfreshPlayButton(screen)
				return
			}
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}
			// For live screencast, set UI state immediately on successful load.
			// Relying only on status polling can leave button text at "Cast" for a while.
			screen.setActiveDevice(sessionDevice)
			screen.updateScreenState("Playing")
			setPlayPauseView("Pause", screen)
			screen.configureImageAutoSkipTimer(mediaType, screen.mediafile)
		}()

		go chromecastStatusWatcher(serverStoppedCTX, screen, actionID)
		return
	}

	var mediaURL string
	var mediaType string
	serverStoppedCTX := context.Background()
	subtitleHost := ""

	if screen.ExternalMediaURL.Checked {
		screen.setCurrentArtwork(nil)
		mediaURL = screen.MediaText.Text
		screen.mediafile = mediaURL

		if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
			mediaType = "application/vnd.apple.mpegurl"
			screen.SetMediaType(mediaType)
			transcode = true
		} else {
			mediaURLinfo, inferredMediaType, err := utils.StreamURLWithMime(context.Background(), mediaURL)
			if err != nil {
				check(screen, err)
				startAfreshPlayButton(screen)
				return
			}
			mediaType = inferredMediaType
			mediaURLinfo.Close()

			wasTranscode := transcode
			transcode = playback.ChromecastExternalURLPolicy(mediaURL, mediaType, transcode, screen.subsfile).Transcode
			if wasTranscode && !transcode {
				screen.Transcode = false
				fyne.Do(func() {
					if screen.TranscodeCheckBox != nil && screen.TranscodeCheckBox.Checked {
						screen.TranscodeCheckBox.SetChecked(false)
					}
				})
			}

			// Set casting media type
			screen.SetMediaType(mediaType)

			if sessionDevice.isAudioOnly && (strings.Contains(mediaType, "video") || strings.Contains(mediaType, "image")) {
				check(screen, errors.New(lang.L("Video/Image file not supported by audio-only device")))
				startAfreshPlayButton(screen)
				return
			}
		}

		if transcode {
			whereToListen, err := utils.URLtoListenIPandPort(sessionDevice.addr)
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
			_, hasSubtitles := playback.ChromecastSubtitlePath(screen.subsfile)
			if hasSubtitles {
				whereToListen, err := utils.URLtoListenIPandPort(sessionDevice.addr)
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
				subtitleHost = whereToListen

				go func() {
					screen.httpserver.StartServing(serverStarted)
					serverCTXStop()
				}()

				if err := <-serverStarted; err != nil {
					check(screen, err)
					startAfreshPlayButton(screen)
					return
				}
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

	} else if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
		// RTMP Mode: No need for local file checks
		mediaType = "application/vnd.apple.mpegurl"
		screen.SetMediaType(mediaType)
	} else {
		// LOCAL FILE: Serve via internal HTTP server
		detectedMediaType, err := utils.GetMimeDetailsFromPath(screen.mediafile)
		if err != nil {
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}
		mediaType = detectedMediaType
		artworkAsset = screen.resolveCurrentGUIArtwork(screen.mediafile, mediaType, true)

		// Chromecast handles images and audio natively - never transcode these
		mediaTypeSlice := strings.Split(mediaType, "/")
		if len(mediaTypeSlice) > 0 && (mediaTypeSlice[0] == "image" || mediaTypeSlice[0] == "audio") {
			transcode = false
		}

		storedResume := screen.prepareResumeSession(mediaType)
		ffmpegSeek = computeChromecastResumeStart(ffmpegSeek, storedResume)
		screen.ffmpegSeek = 0
		if transcode {
			screen.ffmpegSeek = ffmpegSeek
		}

		// Set casting media type
		screen.SetMediaType(mediaType)

		if sessionDevice.isAudioOnly && (strings.Contains(mediaType, "video") || strings.Contains(mediaType, "image")) {
			check(screen, errors.New(lang.L("Video/Image file not supported by audio-only device")))
			startAfreshPlayButton(screen)
			return
		}

		whereToListen, err := utils.URLtoListenIPandPort(sessionDevice.addr)
		if err != nil {
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}

		if screen.httpserver != nil {
			screen.httpserver.StopServer()
		}

		screen.httpserver = httphandlers.NewServer(whereToListen)
		registerGUIArtwork(screen.httpserver, artworkAsset)
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
		if subtitleHost == "" {
			mediaURLParsed, err := url.Parse(mediaURL)
			if err == nil {
				subtitleHost = mediaURLParsed.Host
			}
		}
		if subtitlesPath, ok := playback.ChromecastSubtitlePath(screen.subsfile); ok && subtitleHost != "" {
			ext := strings.ToLower(filepath.Ext(subtitlesPath))
			switch ext {
			case ".srt":
				webvttData, err := utils.ConvertSRTtoWebVTT(subtitlesPath)
				if err != nil {
					check(screen, fmt.Errorf("subtitle conversion: %w", err))
				} else {
					screen.httpserver.AddHandler("/subtitles.vtt", nil, nil, webvttData)
					subtitleURL = "http://" + subtitleHost + "/subtitles.vtt"
				}
			case ".vtt":
				screen.httpserver.AddHandler("/subtitles.vtt", nil, nil, subtitlesPath)
				subtitleURL = "http://" + subtitleHost + "/subtitles.vtt"
			}
		}
	}

	// Load media and update UI on success
	go func() {
		// Use LIVE stream type for URL streams (DMR shows LIVE badge, but buffer unchanged)
		live := screen.ExternalMediaURL.Checked
		listenAddress := ""
		if parsedMediaURL, err := url.Parse(mediaURL); err == nil {
			listenAddress = parsedMediaURL.Host
		}
		if err := client.LoadMedia(castprotocol.LoadRequest{
			MediaURL:    mediaURL,
			ContentType: mediaType,
			Metadata:    guiMediaMetadata(chromecastMediaTitle(screen, mediaURL), listenAddress, artworkAsset),
			StartTime:   ffmpegSeek,
			Duration:    screen.mediaDuration,
			SubtitleURL: subtitleURL,
			Live:        live,
		}); err != nil {
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}
			check(screen, fmt.Errorf("chromecast load: %w", err))
			startAfreshPlayButton(screen)
			return
		}
		if !screen.isChromecastActionCurrent(actionID) {
			return
		}
		screen.setActiveDevice(sessionDevice)
		screen.updateScreenState("Playing")
		setPlayPauseView("Pause", screen)
		armChromecastImageAutoSkipAfterReady(screen, client, actionID, mediaType, screen.mediafile)
	}()

	go chromecastStatusWatcher(serverStoppedCTX, screen, actionID)
}

// chromecastTranscodedSeek performs a seek on transcoded Chromecast streams
// by restarting the HTTP server with new seek position while keeping the connection open.
// This is much faster than stopAction+playAction which closes/reopens the connection.
// Runs fully async to prevent UI freeze during buffering.
func chromecastTranscodedSeek(screen *FyneScreen, seekPos int) {
	releasePermit, permitted := screen.rendererPermit(true)
	if !permitted {
		return
	}

	actionID := screen.nextChromecastActionID()

	// Capture client reference before async operation
	client := screen.activeChromecastPlaybackClient()
	if client == nil || !client.IsConnected() {
		releasePermit()
		return
	}
	// Update seek position immediately (used by status watcher)
	screen.ffmpegSeek = seekPos
	// Run entire seek operation in background to prevent UI freeze
	go func() {
		defer releasePermit()
		// Stop HTTP server (kills FFmpeg) but keep Chromecast client connected
		if screen.httpserver != nil {
			screen.httpserver.StopServer()
		}
		// Transcoded streams always output video/mp4
		mediaType := "video/mp4"
		sessionDevice := screen.getActiveDevice()
		if sessionDevice.addr == "" {
			sessionDevice = screen.selectedDevice
		}
		whereToListen, err := utils.URLtoListenIPandPort(sessionDevice.addr)
		if err != nil {
			check(screen, err)
			return
		}
		// Create new HTTP server with new seek position
		screen.httpserver = httphandlers.NewServer(whereToListen)
		artworkAsset := screen.getCurrentArtwork()
		registerGUIArtwork(screen.httpserver, artworkAsset)
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
		if err := client.LoadMediaOnExisting(castprotocol.LoadRequest{
			MediaURL:    mediaURL,
			ContentType: mediaType,
			Metadata:    guiMediaMetadata(chromecastMediaTitle(screen, mediaURL), whereToListen, artworkAsset),
			Duration:    screen.mediaDuration,
		}); err != nil {
			check(screen, fmt.Errorf("chromecast seek load: %w", err))
			return
		}
		// Restart status watcher
		go chromecastStatusWatcher(serverStoppedCTX, screen, actionID)
	}()
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
// Triggers auto-play next via Fini() when media ends, consistent with DLNA.
func chromecastStatusWatcher(ctx context.Context, screen *FyneScreen, actionID uint64) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// Track actual playback start. BUFFERING alone is not enough for live streams
	// (screencast may briefly report IDLE after initial buffering before PLAYING).
	var mediaStarted bool

	// Stall detection state for the near-end safety net below.
	stallLastTime := -1.0
	stallDuration := 0.0
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
			screen.mu.RLock()
			isScreencast := screen.Screencast
			screen.mu.RUnlock()

			status, err := client.GetStatus()
			if err != nil {
				statusErrs++
				if statusErrs < chromecastLostConnPolls {
					continue
				}
				if !screen.isChromecastActionCurrent(actionID) {
					return
				}
				nearEnd := mediaStarted && !isScreencast &&
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
					// Only reset UI if not looping or auto-playing next
					if !screen.Medialoop && !screen.NextMediaCheck.Checked {
						startAfreshPlayButton(screen)
					}
					return
				}

				if isScreencast {
					server := screen.httpserver
					screen.httpserver = nil
					if screen.cancelServerStop != nil {
						screen.cancelServerStop()
						screen.cancelServerStop = nil
					}
					go func() {
						if server != nil {
							server.StopServer()
						}
						stopScreencastSession(screen)
					}()
					startAfreshPlayButton(screen)
					return
				}

				// Mid-media status loss is not completion.
				startAfreshPlayButton(screen)
				return
			}
			statusErrs = 0
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}

			screen.mu.RLock()
			currentCastingType := screen.castingMediaType
			screen.mu.RUnlock()
			if strings.HasPrefix(currentCastingType, "image/") && chromecastImageStatusReady(status) {
				screen.refreshImageAutoSkipTimer()
			}

			// Update state based on player state
			switch status.PlayerState {
			case "BUFFERING":
				// Media is loading - don't mark as started yet.
				// Some live sessions can bounce BUFFERING->IDLE before first PLAYING.
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
				screen.refreshImageAutoSkipTimer()
			case "PAUSED":
				mediaStarted = true
				startupIdleTicks = 0
				if screen.getScreenState() != "Paused" {
					setPlayPauseView("Play", screen)
					screen.updateScreenState("Paused")
				}
			case "IDLE":
				// Only treat IDLE as "finished" if media had actually started playing
				// Ignore initial IDLE states while media is loading
				if mediaStarted {
					if !screen.isChromecastActionCurrent(actionID) {
						return
					}

					// Screencast is a live session with no natural "end".
					// Ignore transient IDLE reports to avoid accidental replay loops.
					if isScreencast {
						continue
					}

					// Media finished - trigger auto-play next or loop via Fini()
					screen.Fini()

					// Only reset UI if not looping or auto-playing next
					if !screen.Medialoop && !screen.NextMediaCheck.Checked {
						startAfreshPlayButton(screen)
					}
					return
				}
				// If we haven't started yet, just ignore IDLE
				startupIdleTicks++
				if startupIdleTicks >= chromecastStartupIdleTicks {
					if !screen.isChromecastActionCurrent(actionID) {
						return
					}
					if screen.chromecastClient == client {
						screen.chromecastClient = nil
					}
					go client.Close(false)
					if isScreencast {
						server := screen.httpserver
						screen.httpserver = nil
						if screen.cancelServerStop != nil {
							screen.cancelServerStop()
							screen.cancelServerStop = nil
						}
						go func() {
							if server != nil {
								server.StopServer()
							}
							stopScreencastSession(screen)
						}()
						startAfreshPlayButton(screen)
						return
					}
					if screen.httpserver != nil {
						server := screen.httpserver
						screen.httpserver = nil
						go server.StopServer()
					}
					if screen.cancelServerStop != nil {
						screen.cancelServerStop()
						screen.cancelServerStop = nil
					}
					if screen.NextMediaCheck.Checked || screen.Medialoop {
						screen.Fini()
						return
					}
					startAfreshPlayButton(screen)
					return
				}
			}

			// For transcoded streams, use stored duration from ffprobe (Chromecast only knows buffered duration).
			currentTime, duration := chromecastProgressTimeline(
				screen.mediaDuration,
				screen.ffmpegSeek,
				status.Duration,
				status.CurrentTime,
			)

			// Chromecast reports 0 duration/time during buffering, so only
			// non-buffering reports with a duration carry a usable position.
			sampleValid := status.PlayerState != "BUFFERING" && duration > 0

			if sampleValid && mediaStarted && !screen.sliderActive {
				// Display only: ffprobe durations can undershoot, letting the
				// position pass the reported end. The stall net keeps the raw
				// values so real progress past the end never reads as a wedge.
				shownTime := min(currentTime, duration)
				progress := (shownTime / duration) * screen.SlideBar.Max
				fyne.Do(func() {
					screen.SlideBar.SetValue(progress)
					// Update time labels
					current := utils.SecondsToClockTime(int(shownTime))
					total := utils.SecondsToClockTime(int(duration))
					screen.CurrentPos.Set(current)
					screen.EndPos.Set(total)
				})
				screen.persistResumeProgress(int(shownTime), duration, false)
			}

			// Near-end stall safety net: natural completion is detected via
			// the IDLE state above once the receiver tears the session down.
			// This catches sessions that wedge close to the end of the
			// stream instead: a frozen PLAYING position, a BUFFERING state
			// that never receives more data, or a session that stops
			// reporting a duration. A healthy video advances on every poll.
			if !mediaStarted || isScreencast {
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
			// Only reset UI if not looping or auto-playing next
			if !screen.Medialoop && !screen.NextMediaCheck.Checked {
				startAfreshPlayButton(screen)
			}
			return
		}
	}
}

func startAfreshPlayButton(screen *FyneScreen) {
	// Prevent late Chromecast goroutines from restoring playback UI after reset.
	screen.nextChromecastActionID()

	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}
	screen.cancelImageAutoSkipTimer()
	screen.clearActiveDevice()

	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")

	// Reset slider and times (needed for Chromecast which doesn't use sliderUpdate loop)
	fyne.Do(func() {
		screen.SlideBar.SetValue(0)
		screen.CurrentPos.Set("00:00:00")
		screen.EndPos.Set("00:00:00")
	})

	screen.ffmpegSeek = 0
	screen.mediaDuration = 0
	screen.refreshTraversalControls()
}

func gaplessMediaWatcher(ctx context.Context, screen *FyneScreen, payload *soapcalls.TVPayload) {
	t := time.NewTicker(time.Second)
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
				// Requeue against the current session queue, which is the only
				// source of truth for next/previous/autoplay traversal.
				next, _, err := getNextAutoPlayMediaOrError(screen)
				if err != nil {
					if isTraversalBoundaryError(err) {
						screen.GaplessMediaWatcher = nil
						break out
					}
					check(screen, err)
					fyne.Do(func() {
						screen.NextMediaCheck.SetChecked(false)
					})
					screen.GaplessMediaWatcher = nil
					break out
				}

				if path.Base(nextURI) == utils.ConvertFilename(next) {
					continue
				}

				if nextURI == "" {
					// Stop/skip actions clear these fields from another
					// goroutine before this watcher's context is cancelled,
					// so snapshot both and skip the tick if either is gone.
					tvdata := screen.tvdata
					srv := screen.httpserver
					if tvdata == nil || srv == nil {
						continue
					}

					// No need to check for the error as this is something
					// that we did in previous steps in our workflow
					mPath, _ := url.Parse(tvdata.MediaURL)
					sPath, _ := url.Parse(tvdata.SubtitlesURL)

					// Make sure we clean up after ourselves and avoid
					// leaving any dangling handlers. Given the nextURI is ""
					// we know that the previously playing media entry was
					// replaced by the one in the NextURI entry.
					srv.RemoveHandler(mPath.Path)
					srv.RemoveHandler(sPath.Path)

					mediaPath := payload.MediaPath
					if mediaPath == "" {
						_, mediaPath, err = getNextAutoPlayMediaOrError(screen)
						if err != nil {
							if isTraversalBoundaryError(err) {
								screen.GaplessMediaWatcher = nil
								break out
							}
							check(screen, err)
							fyne.Do(func() {
								screen.NextMediaCheck.SetChecked(false)
							})
							screen.GaplessMediaWatcher = nil
							break out
						}
					}

					screen.promoteQueuedArtwork(guiArtworkIdentity(mediaPath), srv)

					if err := setCurrentMediaPath(screen, mediaPath); err != nil {
						check(screen, err)
						screen.GaplessMediaWatcher = nil
						break out
					}
				}

				newTVPayload, err := queueNext(screen, false)
				if err != nil {
					if isTraversalBoundaryError(err) {
						screen.GaplessMediaWatcher = nil
						break out
					}
					check(screen, err)
					fyne.Do(func() {
						screen.NextMediaCheck.SetChecked(false)
					})
					screen.GaplessMediaWatcher = nil
					break out
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
	clearCurrentMediaSelection(screen)
}

func clearsubsAction(screen *FyneScreen) {
	screen.SelectInternalSubs.ClearSelected()
	screen.SubsText.SetText("")
	screen.subsfile = ""
}

func skipPreviousAction(screen *FyneScreen) {
	skipTraversalAction(screen, -1)
}

func skipNextAction(screen *FyneScreen) {
	skipTraversalAction(screen, 1)
}

func skipTraversalAction(screen *FyneScreen, delta int) {
	if screen.mediafile == "" {
		check(screen, errors.New(lang.L("please select a media file")))
		return
	}

	target := traversalPlaybackTarget(screen)
	if target.device.addr == "" || (target.device.deviceType == devices.DeviceTypeDLNA && target.controlURL == "") {
		check(screen, errors.New(lang.L("please select a device")))
		return
	}

	_, nextMediaPath, err := getAdjacentMedia(screen, delta)
	if err != nil {
		if isTraversalBoundaryError(err) {
			screen.refreshTraversalControls()
			return
		}
		check(screen, err)
		return
	}

	skipToMediaPathOnTargetAction(screen, nextMediaPath, target)
}

func skipToMediaPathAction(screen *FyneScreen, mediaPath string) {
	skipToMediaPathOnTargetAction(screen, mediaPath, traversalPlaybackTarget(screen))
}

func skipToMediaPathOnTargetAction(screen *FyneScreen, mediaPath string, target playbackTarget) {
	releasePermit, permitted := screen.rendererPermit(true)
	if !permitted {
		return
	}
	permitHandedOff := false
	defer func() {
		if !permitHandedOff {
			releasePermit()
		}
	}()

	oldMediaPath := screen.mediafile
	oldArtwork := screen.getCurrentArtwork()
	screen.persistDisplayedResumeProgress(true)
	screen.clearResumeSession()

	fyne.Do(func() {
		screen.PlayPause.Disable()
		if screen.SkipPreviousButton != nil {
			screen.SkipPreviousButton.Disable()
		}
		screen.SkipNextButton.Disable()
	})

	if err := setCurrentMediaPath(screen, mediaPath); err != nil {
		check(screen, err)
		return
	}
	targetMediaPath := screen.mediafile

	// For Chromecast: reuse existing connection for faster skip on the same device.
	client := screen.reusableChromecastClientForDevice(target.device)
	if target.device.deviceType == devices.DeviceTypeChromecast && client != nil {
		// Get media type
		mediaType, err := utils.GetMimeDetailsFromPath(screen.mediafile)
		if err != nil {
			check(screen, err)
			return
		}

		actionID := screen.nextChromecastActionID()

		permitHandedOff = true
		go func() {
			defer releasePermit()
			artworkAsset := screen.resolveCurrentGUIArtwork(targetMediaPath, mediaType, true)
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}
			// Determine if transcoding is enabled
			transcode := screen.Transcode
			ffmpegSeek := 0
			var serverStoppedCTX context.Context

			// Chromecast handles images and audio natively - never transcode these
			mediaTypeSlice := strings.Split(mediaType, "/")
			if len(mediaTypeSlice) > 0 && (mediaTypeSlice[0] == "image" || mediaTypeSlice[0] == "audio") {
				transcode = false
			}

			storedResume := screen.prepareResumeSession(mediaType)
			ffmpegSeek = computeChromecastResumeStart(0, storedResume)
			screen.ffmpegSeek = 0
			if transcode {
				screen.ffmpegSeek = ffmpegSeek
			}

			// Set casting media type
			screen.SetMediaType(mediaType)

			// Get server address. A concurrent stop action may have already
			// cleared the field, in which case there is nothing to skip to.
			server := screen.httpserver
			if server == nil {
				return
			}
			whereToListen := server.GetAddr()

			var mediaURL string
			var subtitleURL string

			if transcode {
				// TRANSCODING PATH: Stop server and restart with new file and transcode options
				server.StopServer()

				// Get actual media duration from ffprobe (Chromecast can't report it for transcoded streams)
				if duration, err := utils.DurationForMediaSeconds(screen.ffmpegPath, targetMediaPath); err == nil {
					screen.mediaDuration = duration
				}

				// Determine subtitle path for burning (only if user selected)
				subsPath := ""
				if screen.subsfile != "" {
					subsPath = screen.subsfile
				}

				tcOpts := &utils.TranscodeOptions{
					FFmpegPath:   screen.ffmpegPath,
					SubsPath:     subsPath,
					SeekSeconds:  ffmpegSeek,
					SubtitleSize: utils.SubtitleSizeMedium,
					LogOutput:    screen.Debug,
				}

				// Create new HTTP server with transcoding
				server = httphandlers.NewServer(whereToListen)
				screen.httpserver = server
				serverStarted := make(chan error)
				var serverCTXStop context.CancelFunc
				serverStoppedCTX, serverCTXStop = context.WithCancel(context.Background())
				screen.serverStopCTX = serverStoppedCTX
				screen.cancelServerStop = serverCTXStop

				go func() {
					server.StartSimpleServerWithTranscode(serverStarted, targetMediaPath, tcOpts)
					serverCTXStop()
				}()

				if err := <-serverStarted; err != nil {
					check(screen, err)
					return
				}

				// Transcoded output is always video/mp4
				mediaType = "video/mp4"
				mediaURL = "http://" + whereToListen + "/" + utils.ConvertFilename(targetMediaPath)
				// Subtitles are burned in during transcoding, no separate URL needed

			} else {
				// NON-TRANSCODING PATH: Just update handlers on existing server
				// Clear stored duration for non-transcoded streams (Chromecast reports it correctly)
				screen.mediaDuration = 0

				// Get subtitle URL if needed (remove old handler first)
				server.RemoveHandler("/subtitles.vtt")
				if screen.subsfile != "" {
					ext := strings.ToLower(filepath.Ext(screen.subsfile))
					switch ext {
					case ".srt":
						webvttData, err := utils.ConvertSRTtoWebVTT(screen.subsfile)
						if err == nil {
							server.AddHandler("/subtitles.vtt", nil, nil, webvttData)
							subtitleURL = "http://" + whereToListen + "/subtitles.vtt"
						}
					case ".vtt":
						server.AddHandler("/subtitles.vtt", nil, nil, screen.subsfile)
						subtitleURL = "http://" + whereToListen + "/subtitles.vtt"
					}
				}

				// Remove old media handler and add new one
				// Handler paths use filepath.Base (decoded) because r.URL.Path is decoded by Go's HTTP server
				// URL uses ConvertFilename (encoded) for valid HTTP URL with special characters
				oldHandlerPath := "/" + filepath.Base(oldMediaPath)
				newHandlerPath := "/" + filepath.Base(targetMediaPath)
				server.RemoveHandler(oldHandlerPath)
				server.AddHandler(newHandlerPath, nil, nil, targetMediaPath)

				// Build media URL using URL-encoded filename (for special chars like brackets)
				mediaURL = "http://" + whereToListen + "/" + utils.ConvertFilename(targetMediaPath)

				// Use existing server context
				serverStoppedCTX = screen.serverStopCTX
			}

			// Set state to Waiting to ensure status watcher triggers UI update when playing starts
			screen.updateScreenState("Waiting")
			registerGUIArtwork(server, artworkAsset)

			if client == nil || !client.IsConnected() {
				return
			}
			if err := client.LoadMediaOnExisting(castprotocol.LoadRequest{
				MediaURL:    mediaURL,
				ContentType: mediaType,
				Metadata:    guiMediaMetadata(chromecastMediaTitle(screen, mediaURL), whereToListen, artworkAsset),
				StartTime:   ffmpegSeek,
				Duration:    screen.mediaDuration,
				SubtitleURL: subtitleURL,
			}); err != nil {
				removeGUIArtworkHandler(server, artworkAsset, oldArtwork)
				if !screen.isChromecastActionCurrent(actionID) {
					return
				}
				check(screen, fmt.Errorf("chromecast load: %w", err))
				startAfreshPlayButton(screen)
				return
			}
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}
			removeGUIArtworkHandler(server, oldArtwork, artworkAsset)
			screen.updateScreenState("Playing")
			setPlayPauseView("Pause", screen)
			armChromecastImageAutoSkipAfterReady(screen, client, actionID, mediaType, targetMediaPath)
			go chromecastStatusWatcher(serverStoppedCTX, screen, actionID)
		}()
		return
	}

	// For DLNA or if Chromecast client not ready: use stop+play
	// We need to stop synchronously to avoid race conditions with PlayAction
	// which might be cancelled by the async StopAction or conflict with it.

	fyne.Do(func() {
		screen.PlayPause.SetText(lang.L("Play") + "  ")
		screen.PlayPause.SetIcon(theme.MediaPlayIcon())
		screen.PlayPause.Refresh()
	})

	// Stop must finish before starting Play1, otherwise some DMRs (e.g. Samsung)
	// reject the transition with AVTransport error 701.
	tvdata := screen.tvdata
	server := screen.httpserver
	screen.tvdata = nil
	screen.httpserver = nil
	screen.updateScreenState("Stopped")
	screen.SetMediaType("")

	permitHandedOff = true
	go func() {
		defer releasePermit()
		if tvdata != nil && tvdata.ControlURL != "" {
			_ = tvdata.SendtoTV("Stop")
		}
		if server != nil {
			server.StopServer()
		}

		playActionOnTarget(screen, target)
	}()
}

func previewmedia(screen *FyneScreen) {
	if screen.mediafile == "" {
		check(screen, errors.New(lang.L("please select a media file")))
		return
	}

	mediaType, err := utils.GetMimeDetailsFromPath(screen.mediafile)
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
			img.SetMinSize(fyne.NewSize(imagePreviewMinWidth, imagePreviewMinHeight))
			imgw := fyne.CurrentApp().NewWindow(filepath.Base(screen.mediafile))
			imgw.SetContent(img)
			imgw.Resize(fyne.NewSize(imagePreviewStartWidth, imagePreviewStartHeight))
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
	stopActionInternal(screen, false)
}

// stopActionSync behaves like stopAction but waits for the DLNA network
// teardown (Stop call and HTTP server shutdown) to complete before returning.
// The transcoded seek restart needs this ordering: a Stop that races with the
// next session's SetAVTransportURI/Play makes renderers drop the new session.
func stopActionSync(screen *FyneScreen) {
	stopActionInternal(screen, true)
}

func stopActionInternal(screen *FyneScreen, wait bool) {
	// Silent when the remote session holds the renderer: no GUI session can
	// exist then, so there is nothing to stop.
	releasePermit, permitted := screen.rendererPermit(false)
	if !permitted {
		return
	}
	permitHandedOff := false
	defer func() {
		if !permitHandedOff {
			releasePermit()
		}
	}()

	screen.persistDisplayedResumeProgress(true)
	screen.clearResumeSession()
	chromecastClient := screen.chromecastSessionClient()

	screen.nextChromecastActionID()
	screen.cancelImageAutoSkipTimer()
	screen.clearActiveDevice()
	screen.resetQueuedArtworkState()

	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")

	// Clear casting media type immediately
	screen.SetMediaType("")

	if chromecastClient != nil {
		// Capture references before clearing
		server := screen.httpserver

		if screen.cancelServerStop != nil {
			screen.cancelServerStop()
			screen.cancelServerStop = nil
		}
		screen.serverStopCTX = nil

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
		permitHandedOff = true
		go func() {
			defer releasePermit()
			_ = chromecastClient.Stop()
			chromecastClient.Close(false)
			if server != nil {
				server.StopServer()
			}
			stopScreencastSession(screen)
		}()
		return
	}

	if screen.tvdata == nil || screen.tvdata.ControlURL == "" {
		stopScreencastSession(screen)
		return
	}

	// Capture references before clearing
	tvdata := screen.tvdata
	server := screen.httpserver

	// Clear references immediately
	screen.tvdata = nil
	screen.httpserver = nil

	teardown := func() {
		if tvdata != nil && tvdata.ControlURL != "" {
			_ = tvdata.SendtoTV("Stop")
		}
		if server != nil {
			server.StopServer()
		}
		stopScreencastSession(screen)
	}

	if wait {
		teardown()
		return
	}

	// Run blocking network operations in background
	permitHandedOff = true
	go func() {
		defer releasePermit()
		teardown()
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
	releasePermit, permitted := screen.rendererPermit(true)
	if !permitted {
		return
	}
	go func() {
		defer releasePermit()
		// Handle Chromecast volume for selected device.
		if screen.selectedDeviceType == devices.DeviceTypeChromecast {
			client, cleanup, err := selectedChromecastControlClient(screen)
			if err != nil {
				check(screen, errors.New(lang.L("chromecast not connected")))
				return
			}
			defer cleanup()

			// Get current volume from status
			status, err := client.GetStatus()
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

			if err := client.SetVolume(newVolume); err != nil {
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
	releasePermit, permitted := screen.rendererPermit(false)
	if !permitted {
		return nil, errRemoteLeaseHeld
	}
	defer releasePermit()

	if screen.tvdata == nil {
		return nil, errors.New("queueNext, nil tvdata")
	}

	if clear {
		if err := screen.tvdata.SendtoTV("ClearQueue"); err != nil {
			return nil, err
		}
		screen.clearQueuedArtwork(screen.httpserver)

		return nil, nil
	}

	fname, fpath, err := getNextAutoPlayMediaOrError(screen)
	if err != nil {
		return nil, err
	}
	_, spath := getNextPossibleSubs(fpath)

	var mediaType string
	var isSeek bool

	mediaType, err = utils.GetMimeDetailsFromPath(fpath)
	if err != nil {
		return nil, err
	}

	if !screen.Transcode {
		isSeek = true
	}
	artworkIdentity, artworkAsset := screen.resolveCachedGUIArtwork(fpath, mediaType, true)

	var mediaFile any = fpath
	mediaDuration := 0.0
	if screen.Transcode {
		if duration, probeErr := utils.DurationForMediaSeconds(screen.ffmpegPath, fpath); probeErr == nil && duration > 0 {
			mediaDuration = duration
		}
	}
	oldMediaURL, err := url.Parse(screen.tvdata.MediaURL)
	if err != nil {
		return nil, err
	}

	oldSubsURL, err := url.Parse(screen.tvdata.SubtitlesURL)
	if err != nil {
		return nil, err
	}

	nextTvData := &soapcalls.TVPayload{
		ControlURL:                  screen.tvdata.ControlURL,
		EventURL:                    screen.tvdata.EventURL,
		RenderingControlURL:         screen.tvdata.RenderingControlURL,
		ConnectionManagerURL:        screen.tvdata.ConnectionManagerURL,
		MediaURL:                    "http://" + oldMediaURL.Host + "/" + utils.ConvertFilename(fname),
		SubtitlesURL:                "http://" + oldSubsURL.Host + "/" + utils.ConvertFilename(spath),
		CallbackURL:                 screen.tvdata.CallbackURL,
		MediaType:                   mediaType,
		MediaPath:                   fpath,
		CurrentTimers:               make(map[string]*time.Timer),
		MediaRenderersStates:        make(map[string]*soapcalls.States),
		InitialMediaRenderersStates: make(map[string]bool),
		Transcode:                   screen.Transcode,
		Seekable:                    isSeek,
		MediaDuration:               mediaDuration,
		LogOutput:                   screen.Debug,
		FFmpegPath:                  screen.ffmpegPath,
		FFmpegSubsPath:              spath,
		Metadata:                    guiMediaMetadata("", oldMediaURL.Host, artworkAsset),
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

	if screen.httpserver == nil {
		return nil, errors.New("queueNext, nil httpserver")
	}
	screen.httpserver.AddHandler(mURL.Path, nextTvData, nil, mediaFile)
	screen.httpserver.AddHandler(sURL.Path, nil, nil, spath)
	registerGUIArtwork(screen.httpserver, artworkAsset)

	_, oldQueuedArtwork := screen.queuedArtworkSnapshot()
	if err := nextTvData.SendtoTV("Queue"); err != nil {
		removeGUIArtworkHandler(screen.httpserver, artworkAsset, screen.getCurrentArtwork(), oldQueuedArtwork)
		return nil, err
	}
	oldQueuedArtwork, currentArtwork := screen.commitQueuedArtwork(artworkIdentity, artworkAsset)
	removeGUIArtworkHandler(screen.httpserver, oldQueuedArtwork, currentArtwork, artworkAsset)

	return nextTvData, nil
}

func startRTMPServer(screen *FyneScreen) {
	// RTMP can end in a cast; it is renderer-mutating for exclusivity purposes.
	releasePermit, permitted := screen.rendererPermit(true)
	if !permitted {
		fyne.Do(func() {
			screen.rtmpServerCheck.SetChecked(false)
		})
		return
	}
	permitHandedOff := false
	defer func() {
		if !permitHandedOff {
			releasePermit()
		}
	}()

	screen.rtmpMu.Lock()
	defer screen.rtmpMu.Unlock()

	if screen.rtmpServer != nil {
		return
	}

	screen.rtmpServerCheck.Disable()

	permitHandedOff = true
	go func() {
		defer releasePermit()
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
				check(screen, formatRTMPServerWaitError(err))
				stopRTMPServer(screen)
				stopAction(screen)
			}
		}()

		// Successful start - Update UI
		fyne.Do(func() {
			screen.rtmpServerCheck.Enable()
			screen.rtmpPrevExternalMediaURL = screen.ExternalMediaURL.Checked
			if screen.LoopSelectedCheck != nil {
				screen.rtmpPrevLoop = screen.LoopSelectedCheck.Checked
				screen.LoopSelectedCheck.SetChecked(false)
				screen.LoopSelectedCheck.Disable()
			}
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
			if screen.ScreencastCheckBox != nil {
				screen.ScreencastCheckBox.SetChecked(false)
				screen.ScreencastCheckBox.Disable()
			}
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

func formatRTMPServerWaitError(err error) error {
	if err == nil {
		return errors.New(lang.L("RTMP server stopped unexpectedly"))
	}

	if rtmp.IsListenTimeoutError(err) {
		timeoutMinutes := rtmp.ListenTimeoutSeconds / 60
		return errors.New(fmt.Sprintf(
			"%s\n\n%s",
			lang.L("RTMP server timed out waiting for an incoming stream."),
			fmt.Sprintf(
				lang.L("No stream was received within %d minutes. Start streaming from OBS or another RTMP client, then enable the RTMP server again."),
				timeoutMinutes,
			),
		))
	}

	return fmt.Errorf("%s: %w", lang.L("RTMP server stopped unexpectedly"), err)
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

	fyne.Do(func() {
		screen.rtmpServerCheck.Disable()
	})

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
	if err := screen.ffmpegStatus(); err == nil {
		if !screen.Screencast {
			screen.TranscodeCheckBox.Enable()
		}
		if screen.ScreencastCheckBox != nil {
			screen.ScreencastCheckBox.Enable()
		}
	}
	screen.SlideBar.Enable()
	screen.rtmpURLCard.Hide()
	screen.rtmpURLEntry.SetText("")
	screen.rtmpKeyEntry.SetText("")

	if screen.rtmpPrevExternalMediaURL {
		restoreMediaInputState(screen, screen.rtmpPrevMediaFile, screen.rtmpPrevMediaText)
	}
	if screen.LoopSelectedCheck != nil {
		screen.LoopSelectedCheck.SetChecked(screen.rtmpPrevLoop)
		if !screen.ExternalMediaURL.Checked && (screen.NextMediaCheck == nil || !screen.NextMediaCheck.Checked) {
			screen.LoopSelectedCheck.Enable()
		} else {
			screen.LoopSelectedCheck.Disable()
		}
	}

	screen.updateFFmpegDependentCheckTooltips()
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ticker := time.NewTicker(time.Second)
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
