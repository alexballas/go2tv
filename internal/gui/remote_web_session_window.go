//go:build !(android || ios)

package gui

import (
	"context"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/container"
	fynedialog "github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/layout"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	xfilepicker "github.com/alexballas/xfilepicker/dialog"

	"go2tv.app/go2tv/v2/internal/mediamodel"
)

const (
	remoteRootsPref    = "RemoteWebSessionRoots"
	remoteExposurePref = "RemoteWebSessionExposure"
	remoteAddressPref  = "RemoteWebSessionAddress"
	remotePortPref     = "RemoteWebSessionPort"
	remoteDebugPref    = "RemoteWebSessionDebug"

	remoteExposureLocal = "local"
	remoteExposureLAN   = "lan"

	remoteDefaultPort = "9666"

	remoteShutdownGrace = 15 * time.Second
)

// beginGUIShutdown starts app cleanup exactly once, off the UI goroutine, and
// returns the shared done channel. The managed child is stopped and reaped
// before RTMP/screencast teardown completes.
func (s *FyneScreen) beginGUIShutdown() <-chan struct{} {
	s.shutdownOnce.Do(func() {
		if s.remoteSessionUpdatesDone != nil {
			s.remoteSessionUpdatesDone()
		}
		go func() {
			defer close(s.shutdownDone)
			ctx, cancel := context.WithTimeout(context.Background(), remoteShutdownGrace)
			_ = s.remoteSession.Shutdown(ctx)
			cancel()
			s.rtmpMu.Lock()
			if s.rtmpServer != nil {
				s.rtmpServer.Stop()
			}
			s.rtmpMu.Unlock()
			stopScreencastSession(s)
		}()
	})
	return s.shutdownDone
}

// recomputeRendererControls recalculates renderer-control availability from
// current state. It never blindly restores captured widget state: while the
// remote lease is held everything renderer-mutating is disabled, afterwards
// the normal rules decide.
func (s *FyneScreen) recomputeRendererControls() {
	held := s.renderGate.remoteLeaseHeld()
	var ffmpegErr error
	if !held {
		ffmpegErr = s.ffmpegStatus()
	}
	fyne.Do(func() {
		s.refreshRemoteSessionStatus()
		if held {
			s.Hotkeys = false
			if s.queueWindow != nil {
				s.queueWindow.Hide()
			}
		} else if s.tabs != nil && s.tabs.Selected() != nil {
			s.Hotkeys = s.tabs.Selected().Text == "Go2TV"
		}
		setEnabled := func(disableable fyne.Disableable, enabled bool) {
			if disableable == nil {
				return
			}
			if enabled {
				disableable.Enable()
			} else {
				disableable.Disable()
			}
		}
		if held {
			setEnabled(s.PlayPause, false)
			setEnabled(s.Stop, false)
			setEnabled(s.VolumeUp, false)
			setEnabled(s.VolumeDown, false)
			setEnabled(s.MuteUnmute, false)
			setEnabled(s.SlideBar, false)
			setEnabled(s.rtmpServerCheck, false)
			setEnabled(s.ScreencastCheckBox, false)
			s.updateActiveDeviceView()
			return
		}
		setEnabled(s.PlayPause, true)
		setEnabled(s.Stop, true)
		setEnabled(s.VolumeUp, true)
		setEnabled(s.VolumeDown, true)
		setEnabled(s.MuteUnmute, true)
		setEnabled(s.SlideBar, s.rtmpServer == nil && !s.Screencast)
		setEnabled(s.rtmpServerCheck, ffmpegErr == nil)
		setEnabled(s.ScreencastCheckBox, ffmpegErr == nil && s.rtmpServer == nil)
		s.updateFFmpegDependentCheckTooltips()
		s.updateActiveDeviceView()
	})
	s.refreshTraversalControls()
}

// currentLANAddresses lists concrete non-loopback IPv4 interface addresses.
// Wildcard, loopback, unspecified, and link-local addresses are excluded.
func currentLANAddresses() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var result []string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ipNet, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			if ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			if !slices.Contains(result, ip.String()) {
				result = append(result, ip.String())
			}
		}
	}
	slices.Sort(result)
	return result
}

// remoteConfigFromPrefs materializes the stored manager configuration. It
// rejects a remembered LAN address that is no longer current.
func (s *FyneScreen) remoteConfigFromPrefs() (remoteSessionConfig, error) {
	prefs := fyne.CurrentApp().Preferences()
	roots := prefs.StringList(remoteRootsPref)
	if len(roots) == 0 {
		return remoteSessionConfig{}, errors.New(lang.L("add at least one media folder"))
	}
	port := strings.TrimSpace(prefs.StringWithFallback(remotePortPref, remoteDefaultPort))
	if port == "" {
		port = remoteDefaultPort
	}
	cfg := remoteSessionConfig{
		MediaRoots: slices.Clone(roots),
		FFmpegPath: s.ffmpegPath,
		Debug:      prefs.Bool(remoteDebugPref),
		Version:    s.version,
	}
	if prefs.StringWithFallback(remoteExposurePref, remoteExposureLocal) == remoteExposureLAN {
		address := prefs.String(remoteAddressPref)
		if address == "" || !slices.Contains(currentLANAddresses(), address) {
			return remoteSessionConfig{}, errors.New(lang.L("select a current LAN address"))
		}
		cfg.Listen = net.JoinHostPort(address, port)
		cfg.AllowedOrigins = []string{"http://" + net.JoinHostPort(address, port)}
	} else {
		cfg.Listen = net.JoinHostPort("127.0.0.1", port)
	}
	return cfg, nil
}

// startRemoteWebSession implements the rendererControlGate sequence: validate,
// atomically acquire the remote lease, reject conflicting activity under the
// lease, lock renderer controls, then spawn/wait off the UI goroutine.
func (s *FyneScreen) startRemoteWebSession(cfg remoteSessionConfig, parent fyne.Window) {
	releaseLease, err := s.renderGate.acquireRemoteLease()
	if err != nil {
		checkInWindow(s, err, parent)
		return
	}
	failPreflight := func(message string) {
		releaseLease()
		checkInWindow(s, errors.New(message), parent)
	}
	switch s.getScreenState() {
	case "Playing", "Paused", "Waiting":
		failPreflight(lang.L("stop playback before starting a remote session"))
		return
	}
	s.rtmpMu.Lock()
	rtmpActive := s.rtmpServer != nil
	s.rtmpMu.Unlock()
	if rtmpActive || (s.rtmpServerCheck != nil && s.rtmpServerCheck.Checked) {
		failPreflight(lang.L("disable the RTMP server before starting a remote session"))
		return
	}
	if s.Screencast {
		failPreflight(lang.L("disable Cast Desktop before starting a remote session"))
		return
	}

	s.recomputeRendererControls()
	go func() {
		err := s.remoteSession.Start(context.Background(), cfg, func() {
			releaseLease()
			s.recomputeRendererControls()
		})
		if err != nil {
			// Early validation failures never created a run, so the manager
			// could not release the lease; releasing twice is safe.
			releaseLease()
			s.recomputeRendererControls()
			if shouldShowRemoteStartError(err) {
				checkInWindow(s, err, parent)
			}
		}
	}()
}

func shouldShowRemoteStartError(err error) bool {
	return err != nil && !errors.Is(err, errRemoteStoppedBeforeUp) && !errors.Is(err, errRemoteFailureReported)
}

func (s *FyneScreen) stopRemoteWebSession() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), remoteShutdownGrace)
		defer cancel()
		_ = s.remoteSession.Stop(ctx)
	}()
}

// openRemoteWebSessionDialog shows the single reusable manager dialog.
func (s *FyneScreen) openRemoteWebSessionDialog() {
	if s.remoteDialog != nil {
		s.remoteDialog.Show()
		return
	}
	s.buildRemoteWebSessionDialog()
}

func remoteStateLabel(state remoteSessionState) string {
	switch state {
	case remoteSessionStarting:
		return lang.L("Starting")
	case remoteSessionRunning:
		return lang.L("Running")
	case remoteSessionStopping:
		return lang.L("Stopping")
	case remoteSessionFailed:
		return lang.L("Failed")
	default:
		return lang.L("Stopped")
	}
}

func remoteFailureLabel(code string) string {
	switch code {
	case "":
		return ""
	case remoteFailureReadiness:
		return lang.L("the server did not become ready")
	case remoteFailureExited:
		return lang.L("the server exited unexpectedly")
	case remoteFailureForcedStop:
		return lang.L("the server had to be stopped forcefully")
	case remoteFailureProtocol:
		return lang.L("the server sent an invalid response")
	case remoteFailureAddressInUse:
		return lang.L("the selected port is already in use")
	case remoteFailureAddressUnavailable:
		return lang.L("the selected network address is unavailable")
	case remoteFailurePermissionDenied:
		return lang.L("permission denied while opening the selected port")
	case remoteFailureListenFailed:
		return lang.L("the server could not open the selected address and port")
	default:
		return lang.L("the server failed to start")
	}
}

func (s *FyneScreen) buildRemoteWebSessionDialog() {
	prefs := fyne.CurrentApp().Preferences()
	parent := s.Current

	isolationNotice := widget.NewLabel(lang.L("Starts a separate Web UI session with its own playlist, device selection, and playback. While it runs, the main Go2TV tab is replaced by a session status view."))
	isolationNotice.Wrapping = fyne.TextWrapWord

	roots := prefs.StringList(remoteRootsPref)
	selectedRoot := -1
	configurationLocked := false
	updateRemoveButton := func() {}
	rootsList := widget.NewList(
		func() int { return len(roots) },
		func() fyne.CanvasObject {
			path := widget.NewLabel("")
			path.Truncation = fyne.TextTruncateEllipsis
			return container.NewBorder(
				nil,
				nil,
				widget.NewIcon(theme.FolderIcon()),
				widget.NewIcon(theme.NavigateNextIcon()),
				path,
			)
		},
		func(id widget.ListItemID, object fyne.CanvasObject) {
			if id < len(roots) {
				// Stored roots keep the sandbox-valid document-portal path;
				// only the visible text uses the resolved host path.
				object.(*fyne.Container).Objects[0].(*widget.Label).SetText(mediamodel.QueueDisplayPath(roots[id]))
			}
		},
	)
	rootsList.OnSelected = func(id widget.ListItemID) {
		if configurationLocked {
			rootsList.Unselect(id)
			return
		}
		selectedRoot = id
		updateRemoveButton()
	}
	rootsList.OnUnselected = func(widget.ListItemID) {
		selectedRoot = -1
		updateRemoveButton()
	}

	saveRoots := func() {
		prefs.SetStringList(remoteRootsPref, slices.Clone(roots))
		rootsList.Refresh()
	}

	addRootButton := widget.NewButtonWithIcon(lang.L("Add folder"), theme.ContentAddIcon(), func() {
		var resumeHotkeys func()
		fd := xfilepicker.NewFolderOpen(func(lu fyne.ListableURI, err error) {
			if resumeHotkeys != nil {
				defer resumeHotkeys()
			}
			if err != nil {
				fynedialog.ShowError(err, parent)
				return
			}
			if lu == nil {
				return
			}
			path := lu.Path()
			if !slices.Contains(roots, path) {
				roots = append(roots, path)
				saveRoots()
			}
		}, parent)
		resumeHotkeys = suspendHotkeys(s)
		showFilePicker(fd, parent)
	})
	removeRootButton := widget.NewButtonWithIcon(lang.L("Remove"), theme.DeleteIcon(), func() {
		if selectedRoot < 0 || selectedRoot >= len(roots) {
			return
		}
		roots = slices.Delete(roots, selectedRoot, selectedRoot+1)
		selectedRoot = -1
		rootsList.UnselectAll()
		saveRoots()
	})
	updateRemoveButton = func() {
		if configurationLocked || selectedRoot < 0 || selectedRoot >= len(roots) {
			removeRootButton.Disable()
			return
		}
		removeRootButton.Enable()
	}
	updateRemoveButton()

	addressSelect := widget.NewSelect(nil, func(chosen string) {
		if !strings.HasSuffix(chosen, lang.L("(unavailable)")) {
			prefs.SetString(remoteAddressPref, chosen)
		}
	})
	refreshAddressOptions := func() {
		options := currentLANAddresses()
		remembered := prefs.String(remoteAddressPref)
		selected := ""
		if remembered != "" {
			if slices.Contains(options, remembered) {
				selected = remembered
			} else {
				// Keep the unavailable remembered address visible; Start is
				// rejected until a current address is selected.
				options = append(options, remembered+" "+lang.L("(unavailable)"))
			}
		}
		addressSelect.Options = options
		if selected == "" && remembered == "" && len(options) > 0 {
			selected = options[0]
		}
		addressSelect.SetSelected(selected)
		if selected == "" && remembered != "" {
			addressSelect.PlaceHolder = remembered + " " + lang.L("(unavailable)")
		}
		addressSelect.Refresh()
	}
	refreshAddressOptions()

	exposureSelect := widget.NewSelect([]string{lang.L("This computer"), lang.L("Local network")}, func(chosen string) {
		if chosen == lang.L("Local network") {
			prefs.SetString(remoteExposurePref, remoteExposureLAN)
			addressSelect.Enable()
			refreshAddressOptions()
			return
		}
		prefs.SetString(remoteExposurePref, remoteExposureLocal)
		addressSelect.Disable()
	})
	if prefs.StringWithFallback(remoteExposurePref, remoteExposureLocal) == remoteExposureLAN {
		exposureSelect.SetSelected(lang.L("Local network"))
	} else {
		exposureSelect.SetSelected(lang.L("This computer"))
	}

	portEntry := newNumericalEntry()
	portEntry.Text = prefs.StringWithFallback(remotePortPref, remoteDefaultPort)
	portEntry.OnChanged = func(value string) {
		prefs.SetString(remotePortPref, value)
	}

	statusDot := canvas.NewCircle(theme.Color(theme.ColorNameDisabled))
	statusDotWrap := container.NewCenter(container.NewGridWrap(fyne.NewSize(12, 12), statusDot))
	statusLabel := widget.NewLabelWithStyle(remoteStateLabel(remoteSessionStopped), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	statusRow := container.NewHBox(statusDotWrap, statusLabel)
	messageLabel := widget.NewLabel("")
	messageLabel.Wrapping = fyne.TextWrapWord

	sessionButton := widget.NewButtonWithIcon("", theme.MediaPlayIcon(), func() {
		snapshot := s.remoteSession.Snapshot()
		active := snapshot.State != remoteSessionStopped && snapshot.State != remoteSessionFailed
		if active {
			s.stopRemoteWebSession()
			return
		}
		cfg, err := s.remoteConfigFromPrefs()
		if err != nil {
			fynedialog.ShowError(err, parent)
			return
		}
		s.startRemoteWebSession(cfg, parent)
	})
	openButton := widget.NewButtonWithIcon(lang.L("Open in Browser"), theme.ComputerIcon(), func() {
		target := s.remoteSession.Snapshot().URL
		if target == "" {
			return
		}
		if parsed, err := url.Parse(target); err == nil {
			_ = fyne.CurrentApp().OpenURL(parsed)
		}
	})
	openButton.Importance = widget.HighImportance
	copyButton := widget.NewButtonWithIcon(lang.L("Copy URL"), theme.ContentCopyIcon(), func() {
		target := s.remoteSession.Snapshot().URL
		if target != "" {
			fyne.CurrentApp().Clipboard().SetContent(target)
		}
	})
	exportButton := widget.NewButtonWithIcon(lang.L("Export diagnostics")+"…", theme.DownloadIcon(), func() {
		showRemoteDiagnosticsSaveDialog(s, parent)
	})
	exportButton.Importance = widget.LowImportance
	exportHint := widget.NewLabel(lang.L("Includes recent in-memory logs"))
	exportHint.Alignment = fyne.TextAlignCenter
	lockedNotice := widget.NewLabel(lang.L("Stop the session to edit network settings"))
	lockedNotice.Importance = widget.LowImportance
	lockedNotice.Hide()

	configControls := []fyne.Disableable{addRootButton, exposureSelect, addressSelect, portEntry}
	applySnapshot := func(snapshot remoteSessionSnapshot) {
		statusLabel.SetText(remoteStateLabel(snapshot.State))
		if snapshot.LastError != "" {
			messageLabel.SetText(remoteFailureLabel(snapshot.LastError))
			messageLabel.Importance = widget.DangerImportance
		} else {
			messageLabel.SetText(snapshot.URL)
			messageLabel.Importance = widget.MediumImportance
		}
		messageLabel.Refresh()
		switch snapshot.State {
		case remoteSessionRunning:
			statusLabel.Importance = widget.SuccessImportance
			statusDot.FillColor = theme.Color(theme.ColorNameSuccess)
		case remoteSessionStarting, remoteSessionStopping:
			statusLabel.Importance = widget.WarningImportance
			statusDot.FillColor = theme.Color(theme.ColorNameWarning)
		case remoteSessionFailed:
			statusLabel.Importance = widget.DangerImportance
			statusDot.FillColor = theme.Color(theme.ColorNameError)
		default:
			statusLabel.Importance = widget.MediumImportance
			statusDot.FillColor = theme.Color(theme.ColorNameDisabled)
		}
		statusLabel.Refresh()
		statusDot.Refresh()

		active := snapshot.State != remoteSessionStopped && snapshot.State != remoteSessionFailed
		configurationLocked = active
		for _, control := range configControls {
			if active {
				control.Disable()
			} else {
				control.Enable()
			}
		}
		if !active && prefs.StringWithFallback(remoteExposurePref, remoteExposureLocal) != remoteExposureLAN {
			addressSelect.Disable()
		}
		if active {
			rootsList.UnselectAll()
			lockedNotice.SetText(lang.L("Stop the session to edit network settings"))
			lockedNotice.Show()
			sessionButton.SetText(lang.L("Stop Session"))
			sessionButton.SetIcon(theme.MediaStopIcon())
			sessionButton.Importance = widget.DangerImportance
			if snapshot.State == remoteSessionStopping {
				sessionButton.Disable()
			} else {
				sessionButton.Enable()
			}
		} else {
			// Keep this row allocated so state changes do not move the dialog.
			lockedNotice.SetText("")
			lockedNotice.Show()
			sessionButton.SetText(lang.L("Start Session"))
			sessionButton.SetIcon(theme.MediaPlayIcon())
			sessionButton.Importance = widget.HighImportance
			sessionButton.Enable()
		}
		sessionButton.Refresh()
		updateRemoveButton()
		if snapshot.State == remoteSessionRunning && snapshot.URL != "" {
			openButton.Enable()
			copyButton.Enable()
		} else {
			openButton.Disable()
			copyButton.Disable()
		}
	}
	updates, cancelUpdates := s.remoteSession.Subscribe()
	applySnapshot(<-updates)

	rootsPanel := container.New(&remoteMinimumHeightLayout{height: 112}, rootsList)
	mediaButtons := container.NewHBox(addRootButton, removeRootButton)
	networkTitle := widget.NewLabelWithStyle(lang.L("Network"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	networkTitle.SizeName = theme.SizeNameHeadingText
	networkSettings := container.NewVBox(
		container.New(&remoteColumnsLayout{ratio: 0.31}, widget.NewLabel(lang.L("Exposure")), exposureSelect),
		container.New(&remoteColumnsLayout{ratio: 0.31}, widget.NewLabel(lang.L("LAN address")), addressSelect),
		container.New(&remoteColumnsLayout{ratio: 0.31}, widget.NewLabel(lang.L("Port")), portEntry),
		lockedNotice,
	)
	accessContent := container.NewVBox(
		rootsPanel,
		mediaButtons,
		widget.NewSeparator(),
		networkTitle,
		networkSettings,
	)

	sessionActions := container.NewVBox(
		statusRow,
		messageLabel,
		openButton,
		copyButton,
		sessionButton,
	)
	diagnosticsActions := container.NewVBox(widget.NewSeparator(), exportButton, exportHint)
	sessionContent := container.NewBorder(sessionActions, diagnosticsActions, nil, nil, layout.NewSpacer())
	sessionCard := widget.NewCard(lang.L("Session"), "", sessionContent)
	accessCard := widget.NewCard(lang.L("Media access"), "", accessContent)

	content := container.NewBorder(
		container.NewVBox(isolationNotice, widget.NewSeparator()),
		nil,
		nil,
		nil,
		container.New(&remoteColumnsLayout{ratio: 0.38}, sessionCard, accessCard),
	)

	dialogContent := container.NewPadded(content)
	managerDialog := fynedialog.NewCustom(
		lang.L("Remote Web Session"),
		lang.L("Close"),
		dialogContent,
		parent,
	)
	minimumSize := managerDialog.MinSize()
	managerDialog.Resize(fyne.NewSize(max(minimumSize.Width, 920), minimumSize.Height))
	// Closing the manager dialog never stops an active session.
	managerDialog.SetOnClosed(func() {
		cancelUpdates()
		s.remoteSession.clearError()
		if s.remoteDialog == managerDialog {
			s.remoteDialog = nil
		}
	})
	s.remoteDialog = managerDialog
	managerDialog.Show()
	go func() {
		for snapshot := range updates {
			fyne.Do(func() {
				applySnapshot(snapshot)
			})
		}
	}()
}

type remoteColumnsLayout struct {
	ratio float32
}

func (l *remoteColumnsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) != 2 {
		return
	}
	gap := theme.Padding()
	available := max(size.Width-gap, 0)
	firstWidth := available * l.ratio
	firstMin := objects[0].MinSize().Width
	secondMin := objects[1].MinSize().Width
	if firstWidth < firstMin {
		firstWidth = firstMin
	}
	if available-firstWidth < secondMin {
		firstWidth = available - secondMin
	}
	firstWidth = max(min(firstWidth, available), 0)
	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(fyne.NewSize(firstWidth, size.Height))
	objects[1].Move(fyne.NewPos(firstWidth+gap, 0))
	objects[1].Resize(fyne.NewSize(available-firstWidth, size.Height))
}

func (l *remoteColumnsLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) != 2 {
		return fyne.Size{}
	}
	first := objects[0].MinSize()
	second := objects[1].MinSize()
	return fyne.NewSize(first.Width+theme.Padding()+second.Width, max(first.Height, second.Height))
}

type remoteMinimumHeightLayout struct {
	height float32
}

func (l *remoteMinimumHeightLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 1 {
		objects[0].Move(fyne.NewPos(0, 0))
		objects[0].Resize(size)
	}
}

func (l *remoteMinimumHeightLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	if len(objects) != 1 {
		return fyne.Size{}
	}
	size := objects[0].MinSize()
	size.Height = max(size.Height, l.height)
	return size
}
