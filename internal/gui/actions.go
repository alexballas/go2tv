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
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/rtmp"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
	"go2tv.app/screencast/hls"
)

const (
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
		_ = client.Close(false)
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
	if len(paths) == 1 && strings.EqualFold(filepath.Ext(paths[0]), ".torrent") {
		fyne.Do(func() { showTorrentDialog(screen, paths[0]) })
		return nil
	}
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
	if screen.nowPlayingPath() != "" {
		currentIndex = -1
	}
	if queue != nil && queue.Len() > 0 {
		for _, item := range queue.Items() {
			addItem(item)
		}
		currentIndex = queue.CurrentIndex()
	} else if screen.nowPlayingPath() == "" && screen.mediafile != "" && (screen.ExternalMediaURL == nil || !screen.ExternalMediaURL.Checked) {
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
	if torrentMediaSelected(screen) && screen.mediafile != absMediaFile {
		cancelTorrent(screen)
	}

	if screen.ExternalMediaURL != nil && screen.ExternalMediaURL.Checked {
		fyne.DoAndWait(func() {
			screen.ExternalMediaURL.SetChecked(false)
		})
	}

	screen.mediafile = absMediaFile
	screen.embeddedSubtitle = nil
	if screen.mpris != nil {
		screen.mpris.refresh()
	}
	screen.currentmfolder = filepath.Dir(absMediaFile)
	screen.syncQueueCurrentWithMedia(absMediaFile)
	screen.setCurrentArtworkTarget(guiArtworkIdentity(absMediaFile))
	if screen.mediaKindForPath(absMediaFile) == "audio" {
		go screen.resolveCurrentGUIArtwork(absMediaFile, "audio/unknown", true)
	}

	fyne.Do(func() {
		screen.selectArtwork(absMediaFile)
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

	screen.checkChromecastCompatibility()

	screen.refreshQueueStateUI()
	setPlayPauseView("", screen)
	return nil
}

func clearCurrentMediaSelection(screen *FyneScreen) {
	screen.selectArtwork("")
	if screen.MediaText != nil {
		screen.MediaText.SetText("")
	}
	screen.mediafile = ""
	screen.embeddedSubtitle = nil
	screen.checkChromecastCompatibility()
	if screen.mpris != nil {
		screen.mpris.refresh()
	}
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
		checkInWindow(screen, err, w)

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

		checkInWindow(screen, onPaths(screen, paths), w)
	}, w, true)

	if f, ok := fd.(xfilepicker.FilePicker); ok {
		f.SetFilter(storage.NewExtensionFileFilter(append(append([]string(nil), screen.mediaFormats...), ".torrent")))
	}

	if screen.currentmfolder != "" {
		mfileURI := storage.NewFileURI(screen.currentmfolder)
		mfileLister, err := storage.ListerForURI(mfileURI)

		if err != nil || mfileLister == nil {
			checkInWindow(screen, err, w)
			screen.currentmfolder = ""
		} else if f, ok := fd.(xfilepicker.FilePicker); ok {
			f.SetLocation(mfileLister)
		}
	}

	resumeHotkeys = suspendHotkeys(screen)
	showFilePicker(fd, w)
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

	if screen.mediaSelection != nil {
		screen.mediaSelection.subtitles.SetSelected(lang.L(subtitleExternal))
	}
	screen.SelectInternalSubs.ClearSelected()
	screen.subsfile = absSubtitlesFile
	if screen.mediaSelection != nil {
		screen.mediaSelection.refresh()
	}
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
		f.SetFilter(storage.NewExtensionFileFilter(mediamodel.SubtitleExtensions()))
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
	showFilePicker(fd, w)
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
	state := screen.getScreenState()
	if screen.chromecastProbePending.Load() && state != "Playing" && state != "Paused" {
		return
	}
	if screen.deferTorrentPlayback(target) {
		return
	}
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
	mediaPath := screen.mediafile
	startupPath := mediaPath
	if screen.Screencast || screen.ExternalMediaURL.Checked {
		startupPath = ""
	}
	startupCtx, finishStartup, starting := screen.beginPlaybackStartup(startupPath)
	if !starting {
		screen.deferTorrentPlayback(target)
		return
	}
	setPlayPauseView("", screen)
	if target.device.deviceType == devices.DeviceTypeChromecast {
		actionID := screen.nextChromecastActionID()
		permitHandedOff = true
		go func() {
			defer finishStartup()
			defer releasePermit()
			chromecastPlayAction(screen, actionID, target.device, startupCtx)
		}()
		return
	}

	// DLNA timeout mechanism - re-enable play button if no response after 3 seconds
	screen.cancelPlayTimer()
	sessionDevice := target.device

	ctx, cancelEnablePlay := context.WithTimeout(context.Background(), 3*time.Second)
	screen.mu.Lock()
	screen.cancelEnablePlay = cancelEnablePlay
	screen.mu.Unlock()

	permitHandedOff = true
	go func() {
		defer finishStartup()
		defer releasePermit()
		streamCtx, detachStream := playbackStreamContext(startupCtx)
		defer detachStream()
		if startupCtx.Err() != nil {
			return
		}
		// DLNA desktop mirroring (Cast Desktop) uses its own pipeline.
		if screen.Screencast {
			dlnaScreencastPlayAction(streamCtx, screen, target)
			return
		}
		// RTMP wait mechanism
		if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
			if err := waitForRTMPStream(startupCtx, screen); err != nil {
				if startupCtx.Err() != nil {
					return
				}
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
		seekRestart := screen.dlnaSeekRestart
		if seekRestart {
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
				mediaType, err = utils.GetMimeDetailsFromPath(mediaPath)
				check(screen, err)
				if err != nil {
					startAfreshPlayButton(screen)
					return
				}
				if !torrentMediaSelected(screen) {
					screen.resolveCurrentGUIArtwork(mediaPath, mediaType, true)
				}

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

		mediaFile = mediaPath

		if screen.ExternalMediaURL.Checked {
			screen.setCurrentArtwork(nil)
			// We need to define the mediaPath
			// as this is the core item in our structure
			// that defines that something is being streamed.
			// We use its value for many checks in our code.
			mediaPath = screen.MediaText.Text
			fyne.DoAndWait(func() { screen.mediafile = mediaPath })

			if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
				mediaType = "application/vnd.apple.mpegurl"
				screen.SetMediaType(mediaType)
				mediaFile = mediaPath
			} else {
				mediaURL, inferredMediaType, err := utils.StreamURLWithMime(streamCtx, screen.MediaText.Text)
				if startupCtx.Err() != nil {
					if mediaURL != nil {
						_ = mediaURL.Close()
					}
					return
				}
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
		}

		if err := prepareDesktopSubtitles(screen, mediaPath, transcodeEnabled, transcodeEnabled); err != nil {
			if startupCtx.Err() != nil {
				return
			}
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}
		mediaDuration := 0.0
		if transcodeEnabled {
			if duration, probeErr := utils.DurationForMediaSecondsContext(startupCtx, screen.ffmpegPath, mediaPath); probeErr == nil && duration > 0 {
				mediaDuration = duration
			}
		}
		if startupCtx.Err() != nil {
			return
		}
		screen.mediaDuration = mediaDuration
		screen.dlnaQueueMu.Lock()
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
				MediaPath:                   mediaPath,
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
				MediaURL:                    "http://" + whereToListen + "/" + utils.ConvertFilename(mediaPath),
				SubtitlesURL:                "http://" + whereToListen + "/" + utils.ConvertFilename(screen.subsfile),
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
				FFmpegSubsPath:              screen.subsfile,
				FFmpegEmbeddedSubtitle:      screen.embeddedSubtitle,
				TorrentSource:               torrentSubtitleSource(screen.mediafile, transcodeEnabled && !screen.CustomSubsCheck.Checked),
			}
		}
		screen.tvdata.SetContext(streamCtx)
		screen.dlnaQueueMu.Unlock()
		showDLNATranscodeTimeline(screen, screen.tvdata)
		if screen.httpserver != nil {
			screen.httpserver.StopServer()
		}
		screen.resetQueuedArtworkState()

		screen.dlnaQueueMu.Lock()
		screen.httpserver = httphandlers.NewServer(whereToListen)
		screen.dlnaQueueMu.Unlock()
		artworkAsset := screen.getCurrentArtwork()
		registerGUIArtwork(screen.httpserver, artworkAsset)
		screen.tvdata.Metadata = guiMediaMetadata("", whereToListen, artworkAsset)
		if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked {
			screen.httpserver.AddDirectoryHandler("/rtmp/", screen.rtmpHLSURL)
		}

		serverStarted := make(chan error)
		serverStoppedCTX, serverCTXStop := context.WithCancel(context.Background())
		screen.dlnaQueueMu.Lock()
		screen.serverStopCTX, screen.cancelServerStop = serverStoppedCTX, serverCTXStop
		screen.dlnaQueueMu.Unlock()

		// We pass the tvdata here as we need the callback handlers to be able to react
		// to the different media renderer states.
		go func() {
			screen.httpserver.StartServer(serverStarted, mediaFile, screen.subsfile, screen.tvdata, screen)
			serverCTXStop()
		}()

		// Wait for the HTTP server to properly initialize.
		err = <-serverStarted
		if startupCtx.Err() != nil {
			return
		}
		check(screen, err)
		if err != nil {
			stopAction(screen)
			return
		}

		err = screen.tvdata.SendtoTV("Play1")
		if startupCtx.Err() != nil {
			return
		}
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
		if seekRestart {
			screen.notifyMPRISSeek(existingSeek)
		}
		if directResumeSeek > 0 && !screen.tvdata.Transcode {
			screen.applyInitialDLNAResume(screen.tvdata, directResumeSeek)
		}
		if strings.HasPrefix(mediaType, "image/") {
			screen.updateScreenState("Playing")
			setPlayPauseView("Pause", screen)
		}
		screen.configureImageAutoSkipTimer(mediaType, mediaPath)

		gaplessOption := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")
		if screen.NextMediaCheck.Checked && gaplessOption == "Enabled" {
			newTVPayload, err := queueNext(screen, false)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
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

		defer cancelEnablePlay()

		if errors.Is(ctx.Err(), context.Canceled) {
			return
		}

		screen.dlnaQueueMu.Lock()
		payload := screen.tvdata
		screen.dlnaQueueMu.Unlock()
		if payload == nil {
			return
		}

		out, err := payload.GetTransportInfo()
		if err != nil {
			return
		}

		switch out[0] {
		case "PLAYING":
			screen.updateScreenState("Playing")
			setPlayPauseView("Pause", screen)
			screen.refreshImageAutoSkipTimer()
		case "PAUSED_PLAYBACK":
			screen.updateScreenState("Paused")
			setPlayPauseView("Play", screen)
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
func dlnaScreencastPlayAction(ctx context.Context, screen *FyneScreen, target playbackTarget) {
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
	tvdata.SetContext(ctx)
	screen.dlnaQueueMu.Lock()
	screen.tvdata = tvdata

	screen.httpserver = httphandlers.NewServer(whereToListen)
	serverStarted := make(chan error)
	serverStoppedCTX, serverCTXStop := context.WithCancel(context.Background())
	screen.serverStopCTX = serverStoppedCTX
	screen.cancelServerStop = serverCTXStop
	screen.dlnaQueueMu.Unlock()
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
		if ctx.Err() != nil {
			return
		}
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
	if ctx.Err() != nil {
		return
	}

	screen.setActiveDevice(target.device)
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
func chromecastPlayAction(screen *FyneScreen, actionID uint64, sessionDevice devType, startupCtx context.Context) {
	streamCtx, detachStream := playbackStreamContext(startupCtx)
	defer detachStream()
	if startupCtx.Err() != nil || !screen.isChromecastActionCurrent(actionID) {
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
		if err := waitForRTMPStream(startupCtx, screen); err != nil {
			if startupCtx.Err() != nil {
				return
			}
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}
	}

	// Reset seek position for fresh playback (auto-play next file needs this)
	screen.ffmpegSeek = 0
	screen.clearResumeSession()

	screen.captureChromecastSubtitleSettings()
	transcode := screen.Transcode
	ffmpegSeek := screen.ffmpegSeek

	if err := extractChromecastSubtitles(screen); err != nil {
		if startupCtx.Err() != nil {
			return
		}
		check(screen, err)
		startAfreshPlayButton(screen)
		return
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
		client, err = connectChromecastForAction(screen, actionID, sessionDevice)
		if err != nil {
			if !screen.isChromecastActionCurrent(actionID) {
				return
			}
			check(screen, err)
			startAfreshPlayButton(screen)
			return
		}
		if !screen.installChromecastClientForAction(actionID, client) {
			_ = client.Close(false)
			return
		}
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

		func() {
			_, err := loadChromecastForAction(screen, actionID, sessionDevice, client, castprotocol.LoadRequest{
				MediaURL:    mediaURL,
				ContentType: mediaType,
				Metadata:    metadata.Media{Title: chromecastMediaTitle(screen, mediaURL)},
				Live:        true,
			})
			if err != nil {
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
			go chromecastStatusWatcher(serverStoppedCTX, screen, actionID)
		}()

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
			mediaURLinfo, inferredMediaType, err := utils.StreamURLWithMime(streamCtx, mediaURL)
			if startupCtx.Err() != nil {
				if mediaURLinfo != nil {
					_ = mediaURLinfo.Close()
				}
				return
			}
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
				stream, err = utils.StreamURL(streamCtx, mediaURL)
				if startupCtx.Err() != nil {
					if stream != nil {
						_ = stream.Close()
					}
					return
				}
				if err != nil {
					check(screen, err)
					startAfreshPlayButton(screen)
					return
				}
			}

			tcOpts := desktopChromecastTranscodeOptions(screen, 0)

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
		if !torrentMediaSelected(screen) {
			artworkAsset = screen.resolveCurrentGUIArtwork(screen.mediafile, mediaType, true)
		}

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
			if duration, err := utils.DurationForMediaSecondsContext(startupCtx, screen.ffmpegPath, screen.mediafile); err == nil {
				screen.mediaDuration = duration
			}
			if startupCtx.Err() != nil {
				return
			}

			tcOpts = desktopChromecastTranscodeOptions(screen, ffmpegSeek)
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

	// Keep the receiver's subtitle styling identical during transcoding.
	if subtitleHost == "" {
		if parsed, err := url.Parse(mediaURL); err == nil {
			subtitleHost = parsed.Host
		}
	}
	subtitleOffset := 0
	if transcode {
		subtitleOffset = ffmpegSeek
	}
	subtitleURL, err := registerDesktopChromecastSubtitles(screen, screen.httpserver, subtitleHost, subtitleOffset, transcode)
	if startupCtx.Err() != nil {
		return
	}
	if err != nil {
		check(screen, err)
		startAfreshPlayButton(screen)
		return
	}
	torrentSubtitleURL := registerTorrentSubtitles(screen.httpserver, subtitleHost, screen.mediafile,
		!screen.CustomSubsCheck.Checked && subtitleURL == "" && screen.chromecastSubtitleBurnPath(transcode) == "" && screen.chromecastTorrentBurnSource(transcode) == nil && !screen.Screencast &&
			!screen.ExternalMediaURL.Checked && (screen.rtmpServerCheck == nil || !screen.rtmpServerCheck.Checked), subtitleOffset)
	playbackStart := ffmpegSeek
	if transcode && torrentMediaSelected(screen) {
		// FFmpeg already seeks the source; the new receiver stream starts at zero.
		playbackStart = 0
	}

	// Load media and update UI on success
	if startupCtx.Err() != nil {
		return
	}
	func() {
		// Use LIVE stream type for URL streams (DMR shows LIVE badge, but buffer unchanged)
		live := screen.ExternalMediaURL.Checked
		listenAddress := ""
		if parsedMediaURL, err := url.Parse(mediaURL); err == nil {
			listenAddress = parsedMediaURL.Host
		}
		loadedClient, err := loadChromecastForAction(screen, actionID, sessionDevice, client, castprotocol.LoadRequest{
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
			check(screen, fmt.Errorf("chromecast load: %w", err))
			startAfreshPlayButton(screen)
			return
		}
		client = loadedClient
		if !screen.isChromecastActionCurrent(actionID) {
			return
		}
		screen.setActiveDevice(sessionDevice)
		screen.updateScreenState("Playing")
		setPlayPauseView("Pause", screen)
		armChromecastImageAutoSkipAfterReady(screen, client, actionID, mediaType, screen.mediafile)
		go chromecastStatusWatcher(serverStoppedCTX, screen, actionID)
	}()
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
		tcOpts := desktopChromecastTranscodeOptions(screen, seekPos)
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
		subtitleURL, err := registerDesktopChromecastSubtitles(screen, screen.httpserver, whereToListen, seekPos, true)
		if err != nil {
			check(screen, err)
			return
		}
		torrentSubtitleURL := registerTorrentSubtitles(screen.httpserver, whereToListen, screen.mediafile,
			!screen.CustomSubsCheck.Checked && subtitleURL == "" && screen.chromecastSubtitleBurnPath(true) == "" && screen.chromecastTorrentBurnSource(true) == nil, seekPos)
		// live=false because this is local file playback (seeking)
		if err := client.LoadMediaOnExisting(castprotocol.LoadRequest{
			MediaURL:           mediaURL,
			SubtitleURL:        subtitleURL,
			TorrentSubtitleURL: torrentSubtitleURL,
			ContentType:        mediaType,
			Metadata:           guiMediaMetadata(chromecastMediaTitle(screen, mediaURL), whereToListen, artworkAsset),
			Duration:           screen.mediaDuration,
		}); err != nil {
			check(screen, fmt.Errorf("chromecast seek load: %w", err))
			return
		}
		// Restart status watcher
		screen.notifyMPRISSeek(seekPos)
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
					if screen.mpris != nil {
						screen.mpris.refresh()
					}
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

	screen.cancelPlayTimer()
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
			if ctx.Err() != nil {
				screen.GaplessMediaWatcher = nil
				break out
			}

			if nextURI == "NOT_IMPLEMENTED" || gaplessOption == "Disabled" {
				screen.GaplessMediaWatcher = nil
				break out
			}

			if screen.NextMediaCheck.Checked {
				// Requeue against the current session queue, which is the only
				// source of truth for next/previous/autoplay traversal.
				next, _, err := getNextAutoPlayMediaOrError(screen)
				if err != nil {
					if errors.Is(err, context.Canceled) || isTraversalBoundaryError(err) {
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
					screen.dlnaQueueMu.Lock()
					tvdata, srv := screen.tvdata, screen.httpserver
					screen.dlnaQueueMu.Unlock()
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
							if errors.Is(err, context.Canceled) || isTraversalBoundaryError(err) {
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
					screen.setPlayingMediaPath(mediaPath)
				}

				newTVPayload, err := queueNext(screen, false)
				if err != nil {
					if errors.Is(err, context.Canceled) || isTraversalBoundaryError(err) {
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
				screen.dlnaQueueMu.Lock()
				if ctx.Err() != nil || screen.serverStopCTX != ctx || screen.tvdata == nil {
					screen.dlnaQueueMu.Unlock()
					screen.GaplessMediaWatcher = nil
					break out
				}
				screen.tvdata = payload
				screen.dlnaQueueMu.Unlock()
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
	if torrentMediaSelected(screen) {
		cancelTorrent(screen)
		return
	}
	clearCurrentMediaSelection(screen)
}

func clearsubsAction(screen *FyneScreen) {
	screen.embeddedSubtitle = nil
	screen.SelectInternalSubs.ClearSelected()
	screen.subsfile = ""
	if screen.mediaSelection != nil {
		screen.mediaSelection.refresh()
	}
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
	if screen.deferTorrentPlayback(target) {
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
			screen.captureChromecastSubtitleSettings()
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

				tcOpts := desktopChromecastTranscodeOptions(screen, ffmpegSeek)

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

			} else {
				// NON-TRANSCODING PATH: Just update handlers on existing server
				// Clear stored duration for non-transcoded streams (Chromecast reports it correctly)
				screen.mediaDuration = 0

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

			subtitleOffset := 0
			if transcode {
				subtitleOffset = ffmpegSeek
			}
			subtitleURL, err = registerDesktopChromecastSubtitles(screen, server, whereToListen, subtitleOffset, transcode)
			if err != nil {
				check(screen, err)
				return
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
			screen.setPlayingMediaPath(targetMediaPath)
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
	screen.dlnaQueueMu.Lock()
	tvdata, server := screen.tvdata, screen.httpserver
	if screen.cancelServerStop != nil {
		screen.cancelServerStop()
	}
	screen.tvdata, screen.httpserver = nil, nil
	screen.serverStopCTX, screen.cancelServerStop = nil, nil
	screen.dlnaQueueMu.Unlock()
	screen.updateScreenState("Stopped")
	screen.SetMediaType("")

	permitHandedOff = true
	go func() {
		defer releasePermit()
		if tvdata != nil && tvdata.ControlURL != "" {
			// Gapless payloads inherit the canceled server session context.
			tvdata.SetContext(context.Background())
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
			err := open.Run(mediasource.Input(screen.mediafile))
			check(screen, err)
		}()
	}
}

func stopAction(screen *FyneScreen) {
	screen.cancelPendingTorrentPlayback()
	if screen.stopPlaybackStartup() {
		return
	}
	stopActionInternal(screen, false)
}

// Finish renderer and HTTP teardown before starting replacement playback.
func stopActionSync(screen *FyneScreen) {
	if done := screen.cancelPlaybackStartup(); done != nil {
		<-done
	}
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

	screen.dlnaQueueMu.Lock()
	if screen.tvdata != nil && screen.cancelServerStop != nil {
		screen.cancelServerStop()
	}
	screen.dlnaQueueMu.Unlock()

	screen.persistDisplayedResumeProgress(true)
	screen.clearResumeSession()
	screen.nextChromecastActionID()
	chromecastClient := screen.chromecastClientForStop()
	screen.cancelImageAutoSkipTimer()
	screen.clearActiveDevice()
	screen.resetQueuedArtworkState()

	setPlayPauseView("Play", screen)
	screen.updateScreenState("Stopped")

	// Clear casting media type immediately
	screen.SetMediaType("")

	if chromecastClient != nil {
		chromecastClient.Log().Debug("stopping Chromecast session", "Method", "StopAction")
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

		teardown := func() {
			if chromecastClient.IsConnected() {
				_ = chromecastClient.Stop()
			}
			_ = chromecastClient.Close(false)
			if server != nil {
				server.StopServer()
			}
			stopScreencastSession(screen)
		}
		if wait {
			teardown()
		} else {
			permitHandedOff = true
			go func() {
				defer releasePermit()
				teardown()
			}()
		}
		return
	}

	screen.dlnaQueueMu.Lock()
	tvdata, server := screen.tvdata, screen.httpserver
	if tvdata == nil || tvdata.ControlURL == "" {
		screen.dlnaQueueMu.Unlock()
		stopScreencastSession(screen)
		return
	}
	// The canceled server context remains visible until references are cleared.
	screen.tvdata, screen.httpserver = nil, nil
	screen.serverStopCTX, screen.cancelServerStop = nil, nil
	screen.dlnaQueueMu.Unlock()

	teardown := func() {
		if tvdata != nil && tvdata.ControlURL != "" {
			tvdata.SetContext(context.Background())
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
			} else if screen.mpris != nil {
				screen.mpris.volume(float64(newVolume))
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
		} else if screen.mpris != nil {
			screen.mpris.volume(float64(setVolume) / 100)
		}
	}()
}

func queueNext(screen *FyneScreen, clear bool) (*soapcalls.TVPayload, error) {
	releasePermit, permitted := screen.rendererPermit(false)
	if !permitted {
		return nil, errRemoteLeaseHeld
	}
	defer releasePermit()
	screen.dlnaQueueOperationMu.Lock()
	defer screen.dlnaQueueOperationMu.Unlock()

	// Stop can clear or replace the session while preparation performs I/O.
	// Snapshot references and keep all publication checks under the same lock.
	screen.dlnaQueueMu.Lock()
	tvdata, server := screen.tvdata, screen.httpserver
	if tvdata == nil || server == nil {
		screen.dlnaQueueMu.Unlock()
		return nil, errors.New("queueNext, no active DLNA session")
	}
	if screen.serverStopCTX == nil {
		screen.serverStopCTX, screen.cancelServerStop = context.WithCancel(context.Background())
	}
	sessionCtx := screen.serverStopCTX
	ctx, cancel := context.WithCancel(sessionCtx)
	startupCtx := tvdata.Context()
	stopCancelling := context.AfterFunc(startupCtx, cancel)
	if startupCtx.Err() != nil {
		cancel()
	}
	screen.dlnaQueueMu.Unlock()
	defer cancel()
	defer stopCancelling()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if clear {
		clearPayload := &soapcalls.TVPayload{ControlURL: tvdata.ControlURL, PinnedIP: tvdata.PinnedIP}
		clearPayload.SetContext(ctx)
		if err := clearPayload.SendtoTV("ClearQueue"); err != nil {
			return nil, err
		}
		screen.dlnaQueueMu.Lock()
		defer screen.dlnaQueueMu.Unlock()
		if ctx.Err() != nil || screen.tvdata != tvdata || screen.httpserver != server {
			return nil, context.Canceled
		}
		screen.clearQueuedArtwork(server)
		return nil, nil
	}

	fname, fpath, err := getNextAutoPlayMediaOrError(screen)
	if err != nil {
		return nil, err
	}
	automaticSubs := screen.CustomSubsCheck == nil || !screen.CustomSubsCheck.Checked
	spath := ""
	if automaticSubs {
		spath = getNextPossibleSubs(fpath)
	}

	var mediaType string
	var isSeek bool

	mediaType, err = utils.GetMimeDetailsFromPath(fpath)
	if err != nil {
		return nil, err
	}
	var embeddedSubtitle *utils.EmbeddedSubtitle
	// Remove an extracted file unless the queued session accepts ownership.
	ownedSubtitle := ""
	defer func() {
		if ownedSubtitle != "" {
			_ = os.Remove(ownedSubtitle)
		}
	}()
	_, progressive := mediasource.Lookup(fpath)
	if automaticSubs && screen.Transcode && spath == "" && strings.HasPrefix(mediaType, "video/") && !progressive {
		subs, probeErr := utils.GetSubsContext(ctx, screen.ffmpegPath, fpath)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if probeErr == nil {
			tracks := make([]int, len(subs))
			for track := range tracks {
				tracks[track] = track
			}
			spath, embeddedSubtitle, err = prepareEmbeddedSubtitles(ctx, screen.ffmpegPath, fpath, tracks, screen.Transcode, true)
			if err != nil {
				return nil, err
			}
			ownedSubtitle = spath
		}
	}

	if !screen.Transcode {
		isSeek = true
	}
	artworkIdentity, artworkAsset := screen.resolveCachedGUIArtwork(fpath, mediaType, true)

	var mediaFile any = fpath
	mediaDuration := 0.0
	if screen.Transcode {
		if duration, probeErr := utils.DurationForMediaSecondsContext(ctx, screen.ffmpegPath, fpath); probeErr == nil && duration > 0 {
			mediaDuration = duration
		}
	}
	oldMediaURL, err := url.Parse(tvdata.MediaURL)
	if err != nil {
		return nil, err
	}

	oldSubsURL, err := url.Parse(tvdata.SubtitlesURL)
	if err != nil {
		return nil, err
	}

	nextTvData := &soapcalls.TVPayload{
		ControlURL:                  tvdata.ControlURL,
		EventURL:                    tvdata.EventURL,
		RenderingControlURL:         tvdata.RenderingControlURL,
		ConnectionManagerURL:        tvdata.ConnectionManagerURL,
		MediaURL:                    "http://" + oldMediaURL.Host + "/" + utils.ConvertFilename(fname),
		SubtitlesURL:                "http://" + oldSubsURL.Host + "/" + utils.ConvertFilename(spath),
		CallbackURL:                 tvdata.CallbackURL,
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
		FFmpegEmbeddedSubtitle:      embeddedSubtitle,
		TorrentSource:               torrentSubtitleSource(fpath, automaticSubs && screen.Transcode),
		Metadata:                    guiMediaMetadata("", oldMediaURL.Host, artworkAsset),
	}
	nextTvData.SetContext(ctx)
	var subtitles any = spath
	if !screen.Transcode && spath != "" {
		subtitleURL, prepared, err := httphandlers.PrepareDLNASubtitles(nextTvData.Context(), nextTvData.SubtitlesURL, spath, screen.ffmpegPath)
		if err != nil {
			return nil, err
		}
		nextTvData.SubtitlesURL, subtitles = subtitleURL, prepared
	}

	mURL, err := url.Parse(nextTvData.MediaURL)
	if err != nil {
		return nil, err
	}

	sURL, err := url.Parse(nextTvData.SubtitlesURL)
	if err != nil {
		return nil, err
	}

	screen.dlnaQueueMu.Lock()
	if ctx.Err() != nil || screen.tvdata != tvdata || screen.httpserver != server {
		screen.dlnaQueueMu.Unlock()
		return nil, context.Canceled
	}
	server.AddHandler(mURL.Path, nextTvData, nil, mediaFile)
	if !screen.Transcode && sURL.Path != "/." {
		server.AddHandler(sURL.Path, nil, nil, subtitles)
	}
	registerGUIArtwork(server, artworkAsset)
	_, oldQueuedArtwork := screen.queuedArtworkSnapshot()
	screen.dlnaQueueMu.Unlock()

	if err := nextTvData.SendtoTV("Queue"); err != nil {
		if mURL.Path != oldMediaURL.Path {
			server.RemoveHandler(mURL.Path)
		}
		if sURL.Path != "/." && sURL.Path != oldSubsURL.Path {
			server.RemoveHandler(sURL.Path)
		}
		removeGUIArtworkHandler(server, artworkAsset, screen.getCurrentArtwork(), oldQueuedArtwork)
		return nil, err
	}
	screen.dlnaQueueMu.Lock()
	defer screen.dlnaQueueMu.Unlock()
	if ctx.Err() != nil || screen.tvdata != tvdata || screen.httpserver != server {
		if mURL.Path != oldMediaURL.Path {
			server.RemoveHandler(mURL.Path)
		}
		if sURL.Path != "/." && sURL.Path != oldSubsURL.Path {
			server.RemoveHandler(sURL.Path)
		}
		removeGUIArtworkHandler(server, artworkAsset, screen.getCurrentArtwork(), oldQueuedArtwork)
		return nil, context.Canceled
	}
	// The payload outlives this preparation operation.
	nextTvData.SetContext(sessionCtx)
	if ownedSubtitle != "" {
		screen.tempFiles = append(screen.tempFiles, ownedSubtitle)
		ownedSubtitle = ""
	}
	oldQueuedArtwork, currentArtwork := screen.commitQueuedArtwork(artworkIdentity, artworkAsset)
	removeGUIArtworkHandler(server, oldQueuedArtwork, currentArtwork, artworkAsset)

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
			screen.selectArtwork("")
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

func waitForRTMPStream(startupCtx context.Context, screen *FyneScreen) error {
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

	ctx, cancel := context.WithTimeout(startupCtx, 60*time.Second)
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
