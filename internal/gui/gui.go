//go:build !(android || ios)

package gui

import (
	"context"
	"embed"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	fynetooltip "github.com/alexballas/fyne-tooltip"
	ttwidget "github.com/alexballas/fyne-tooltip/widget"
	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/app"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/crashlog"
	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/rtmp"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

// screencastSession abstracts a live screencast capture session.
// Both the Chromecast HLS pipeline (go2tv.app/screencast/hls.Session)
// and the DLNA MPEG-TS pipeline (go2tv.app/screencast/ts.Session) implement it.
type screencastSession interface {
	Close() error
	Done() <-chan error
	StderrTail(n int) string
}

// FyneScreen .
type FyneScreen struct {
	tempFiles                []string
	SelectInternalSubs       *widget.Select
	CurrentPos               binding.String
	EndPos                   binding.String
	serverStopCTX            context.Context
	cancelServerStop         context.CancelFunc
	Current                  fyne.Window
	cancelEnablePlay         context.CancelFunc
	PlayPause                *widget.Button
	Debug                    *debugWriter
	DiscoveryDebug           *debugWriter
	VolumeUp                 *widget.Button
	SkipPreviousButton       *widget.Button
	SkipNextButton           *widget.Button
	tvdata                   *soapcalls.TVPayload
	tabs                     *container.AppTabs
	CheckVersion             *widget.Button
	SubsText                 *widget.Entry
	CustomSubsCheck          *widget.Check
	NextMediaCheck           *widget.Check
	LoopSelectedCheck        *widget.Check
	TranscodeCheckBox        *widget.Check
	ScreencastCheckBox       *widget.Check
	Stop                     *widget.Button
	DeviceList               *deviceList
	httpserver               *httphandlers.HTTPserver
	MediaText                *widget.Entry
	ExternalMediaURL         *widget.Check
	SkinNextOnlySameTypes    bool
	GaplessMediaWatcher      func(context.Context, *FyneScreen, *soapcalls.TVPayload)
	SlideBar                 *tappedSlider
	MuteUnmute               *widget.Button
	VolumeDown               *widget.Button
	selectedDevice           devType
	activeDevice             devType
	selectedDeviceType       string
	chromecastClient         *castprotocol.CastClient // Active Chromecast connection
	chromecastActionID       uint64
	imageAutoSkipID          uint64
	State                    string
	mediafile                string
	version                  string
	eventURL                 string
	subsfile                 string
	controlURL               string
	renderingControlURL      string
	connectionManagerURL     string
	currentmfolder           string
	ffmpegPath               string
	ffmpegSeek               int
	castingMediaType         string  // MIME type of currently casting media (e.g., "image/jpeg", "video/mp4")
	mediaDuration            float64 // Actual media duration in seconds (from ffprobe, for transcoded streams)
	currentArtwork           *metadata.ArtworkAsset
	currentArtworkIdentity   string
	queuedArtwork            *metadata.ArtworkAsset
	queuedArtworkIdentity    string
	artworkCache             map[string]artworkCacheEntry
	chromecastCheckedFile    string // Tracks which file was already auto-checked for Chromecast compatibility
	systemTheme              fyne.ThemeVariant
	mediaFormats             []string
	audioFormats             []string
	videoFormats             []string
	imageFormats             []string
	muError                  sync.RWMutex
	mu                       sync.RWMutex
	ffmpegPathChanged        bool
	ffmpegCheckPath          string
	ffmpegCheckErr           error
	ffmpegCheckValid         bool
	ffmpegCheckDirty         bool
	Medialoop                bool
	sliderActive             bool
	dlnaSeekRestart          bool
	imageAutoSkipTimeout     int
	Transcode                bool
	Screencast               bool
	screencastPrevTranscode  bool
	screencastPrevExternal   bool
	screencastPrevManualSubs bool
	screencastPrevLoop       bool
	screencastPrevNext       bool
	screencastPrevMediaText  string
	screencastPrevMediaFile  string
	screencastSession        screencastSession
	screencastMu             sync.Mutex
	ErrorVisible             bool
	Hotkeys                  bool
	hotkeysSuspendCount      int32
	MediaBrowse              *widget.Button
	QueueButton              *widget.Button
	ClearMedia               *widget.Button
	SubsBrowse               *widget.Button
	SessionQueue             *SessionQueue
	queueWindow              fyne.Window
	queueList                *widget.List
	queueHeader              *widget.Label
	queueDetails             *widget.Label
	queueAddButton           *widget.Button
	queuePlayNowButton       *widget.Button
	queueRemoveButton        *widget.Button
	queueMoveUpButton        *widget.Button
	queueMoveDownButton      *widget.Button
	queueClearButton         *widget.Button
	queueSelectedIndex       int
	queueRevision            uint64
	lastQueueUIState         queueUIState
	queueUIStateValid        bool
	lastQueueTapIndex        int
	lastQueueTapAt           time.Time
	muted                    bool
	ActiveDeviceLabel        *widget.Label
	ActiveDeviceIcon         *widget.Icon
	ActiveDeviceCard         *widget.Card
	rtmpServer               *rtmp.Server
	rtmpServerCheck          *widget.Check
	transcodeToolTipCheck    *ttwidget.Check
	screencastToolTipCheck   *ttwidget.Check
	rtmpServerToolTipCheck   *ttwidget.Check
	rtmpURLCard              *widget.Card
	rtmpURLEntry             *widget.Entry
	rtmpKeyEntry             *widget.Entry
	rtmpHLSURL               string // The local HLS HLS URL
	rtmpPrevExternalMediaURL bool
	rtmpPrevLoop             bool
	rtmpPrevMediaText        string
	rtmpPrevMediaFile        string
	imageAutoSkipMediaPath   string
	imageAutoSkipCancel      context.CancelFunc
	rtmpMu                   sync.Mutex
	resumeSession            resumePlaybackSession
	Crash                    *crashlog.Session
	PendingCrashPath         string
	renderGate               rendererControlGate
	remoteSession            *remoteSessionManager
	remoteSessionStatus      *remoteSessionStatusView
	remoteSessionUpdatesDone func()
	remoteDialog             dialog.Dialog
	shutdownOnce             sync.Once
	shutdownDone             chan struct{}
}

type droppedMediaMode uint8

const (
	droppedMediaModeReplace droppedMediaMode = iota
	droppedMediaModeAppend
)

type devType struct {
	name        string
	addr        string
	deviceType  string
	isAudioOnly bool
}

func (s *FyneScreen) updateFFmpegDependentCheckTooltips() {
	if s == nil {
		return
	}

	ffmpegMissing := s.ffmpegCheckValid && !s.ffmpegCheckDirty && s.ffmpegCheckPath == s.ffmpegPath && s.ffmpegCheckErr != nil
	toolTipMsg := lang.L("ffmpeg is required. install it or update ffmpeg path in Settings")

	setToolTip := func(ttCheck *ttwidget.Check, baseCheck *widget.Check) {
		if ttCheck == nil || baseCheck == nil {
			return
		}
		if ffmpegMissing && baseCheck.Disabled() {
			ttCheck.SetToolTip(toolTipMsg)
			return
		}
		ttCheck.SetToolTip("")
	}

	setToolTip(s.transcodeToolTipCheck, s.TranscodeCheckBox)
	setToolTip(s.screencastToolTipCheck, s.ScreencastCheckBox)
	setToolTip(s.rtmpServerToolTipCheck, s.rtmpServerCheck)
}

func (s *FyneScreen) markFFmpegPathChanged() {
	if s == nil {
		return
	}

	s.ffmpegPathChanged = true
	s.ffmpegCheckDirty = true
}

func (s *FyneScreen) ffmpegStatus() error {
	if s == nil {
		return nil
	}

	if !s.ffmpegCheckValid || s.ffmpegCheckDirty || s.ffmpegCheckPath != s.ffmpegPath {
		return s.validateFFmpeg()
	}

	return s.ffmpegCheckErr
}

func (s *FyneScreen) validateFFmpeg() error {
	if s == nil {
		return nil
	}

	err := utils.CheckFFmpeg(s.ffmpegPath)
	s.ffmpegCheckPath = s.ffmpegPath
	s.ffmpegCheckErr = err
	s.ffmpegCheckValid = true
	s.ffmpegCheckDirty = false
	return err
}

//go:embed translations
var translations embed.FS

// Start .
func Start(ctx context.Context, s *FyneScreen) {
	if s == nil {
		return
	}

	if s.tempFiles == nil {
		s.tempFiles = make([]string, 0)
	}

	defer func() {
		for _, file := range s.tempFiles {
			os.Remove(file)
		}
	}()

	w := s.Current
	w.SetOnDropped(onDropFiles(s))

	tabs := container.NewAppTabs(
		container.NewTabItem("Go2TV", container.NewPadded(mainWindow(s))),
		container.NewTabItem(lang.L("Settings"), container.NewPadded(settingsWindow(s))),
		container.NewTabItem(lang.L("About"), aboutWindow(s)),
	)

	s.Hotkeys = true
	tabs.OnSelected = func(t *container.TabItem) {
		if t.Text == "Go2TV" {
			if s.renderGate.remoteLeaseHeld() {
				// Remote session owns the renderer; availability is recomputed
				// when the lease is released.
				s.Hotkeys = false
				return
			}
			s.Hotkeys = true
			if s.rtmpServer == nil && !s.Screencast {
				s.TranscodeCheckBox.Enable()
				if s.ScreencastCheckBox != nil && !s.Screencast {
					s.ScreencastCheckBox.Enable()
				}
				s.SlideBar.Enable()
			} else if s.Screencast {
				s.TranscodeCheckBox.Disable()
			}

			ffmpegErr := s.ffmpegStatus()
			if ffmpegErr != nil {
				s.TranscodeCheckBox.SetChecked(false)
				s.TranscodeCheckBox.Disable()
				if s.ScreencastCheckBox != nil {
					s.ScreencastCheckBox.SetChecked(false)
					s.ScreencastCheckBox.Disable()
				}
				setInternalSubsDropdownNoSubs(s)
			}

			if ffmpegErr != nil {
				s.rtmpServerCheck.SetChecked(false)
				s.rtmpServerCheck.Disable()
			} else {
				if s.rtmpServer == nil {
					s.rtmpServerCheck.Enable()
				}
			}
			s.updateFFmpegDependentCheckTooltips()

			if s.ffmpegPathChanged {
				if ffmpegErr == nil && s.mediafile != "" {
					selectMediaFile(s, mediaFileURI(s.mediafile))
				}
				s.ffmpegPathChanged = false
			}

			return
		}
		s.Hotkeys = false
	}

	s.ffmpegPathChanged = false

	if err := s.ffmpegStatus(); err != nil {
		s.TranscodeCheckBox.Disable()
		if s.ScreencastCheckBox != nil {
			s.ScreencastCheckBox.Disable()
		}
		s.rtmpServerCheck.Disable()
	}
	s.updateFFmpegDependentCheckTooltips()

	s.tabs = tabs

	w.SetContent(fynetooltip.AddWindowToolTipLayer(tabs, w.Canvas()))
	minSize := tabs.MinSize()
	w.Resize(fyne.NewSize(fyne.Max(1000, minSize.Width), minSize.Height))
	w.CenterOnScreen()
	w.SetMaster()

	devices.StartDiscovery(ctx)

	if app := fyne.CurrentApp(); app != nil {
		app.Lifecycle().SetOnStopped(func() {
			// Non-blocking final safety trigger; never waits on the UI path.
			s.beginGUIShutdown()
			if s.Crash != nil {
				_ = s.Crash.CloseClean()
			}
		})
	}

	go func() {
		<-ctx.Done()
		<-s.beginGUIShutdown()
		if s.Crash != nil {
			_ = s.Crash.CloseClean()
		}
		os.Exit(0)
	}()

	// Main-window close starts cleanup off the UI goroutine and returns
	// immediately; the window closes once the managed child is reaped and
	// RTMP/screencast teardown finished.
	w.SetCloseIntercept(func() {
		done := s.beginGUIShutdown()
		go func() {
			<-done
			fyne.Do(func() {
				w.SetCloseIntercept(nil)
				w.Close()
			})
		}()
	})

	go silentCheckVersion(s)
	showPendingCrashPopup(s)

	w.ShowAndRun()
}

func mediaFileURI(path string) fyne.URI {
	return storage.NewFileURI(path)
}

// EmitMsg Method to implement the screen interface
func (p *FyneScreen) EmitMsg(a string) {
	fyne.Do(func() {
		switch a {
		case "Playing":
			setPlayPauseView("Pause", p)
			p.updateScreenState("Playing")
		case "Paused":
			setPlayPauseView("Play", p)
			p.updateScreenState("Paused")
		case "Stopped":
			stopAction(p)
		default:
			dialog.ShowInformation("?", "Unknown callback value", p.Current)
		}
	})
}

// SetMediaType Method to implement the screen interface
func (p *FyneScreen) SetMediaType(mediaType string) {
	p.mu.Lock()
	p.castingMediaType = mediaType
	p.mu.Unlock()
}

// Fini Method to implement the screen interface.
// Will only be executed when we receive a callback message,
// not when we explicitly click the Stop button.
func (p *FyneScreen) Fini() {
	fyne.Do(func() {
		if p.Screencast {
			return
		}

		gaplessOption := fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled")
		target := autoPlayPlaybackTarget(p)

		// Finished-media transitions should always restart from a stopped state.
		// Otherwise playAction may interpret the follow-up as pause/resume.
		p.updateScreenState("Stopped")

		// For Chromecast, ignore gapless setting (it's DLNA-specific)
		isChromecast := target.device.deviceType == devices.DeviceTypeChromecast

		if p.NextMediaCheck.Checked && (isChromecast || gaplessOption == "Disabled") {
			_, nextMediaPath, err := getNextAutoPlayMediaOrError(p)
			if err != nil {
				if isTraversalBoundaryError(err) {
					startAfreshPlayButton(p)
					return
				}
				check(p, err)
				startAfreshPlayButton(p)
				return
			}

			if isChromecast && p.reusableChromecastClientForDevice(target.device) != nil {
				go skipToMediaPathOnTargetAction(p, nextMediaPath, target)
				return
			}

			if err := setCurrentMediaPath(p, nextMediaPath); err != nil {
				check(p, err)
				startAfreshPlayButton(p)
				return
			}

			go playActionOnTarget(p, target)
			return
		}
		// Main media loop logic
		if p.Medialoop {
			go playActionOnTarget(p, target)
		}
	})
}

func check(s *FyneScreen, err error) {
	checkInWindow(s, err, s.Current)
}

func checkInWindow(s *FyneScreen, err error, parent fyne.Window) {
	s.muError.Lock()
	defer s.muError.Unlock()

	fyne.Do(func() {
		if err != nil && !s.ErrorVisible {
			s.ErrorVisible = true
			cleanErr := strings.ReplaceAll(err.Error(), ": ", "\n")
			e := dialog.NewError(errors.New(cleanErr), parent)
			e.Show()
			e.SetOnClosed(func() {
				s.ErrorVisible = false
			})
		}
	})
}

var (
	errNoNextQueueMedia     = errors.New("no next queued media")
	errNoPreviousQueueMedia = errors.New("no previous queued media")
)

// Traversal is queue-only. Local file selection always creates or updates
// SessionQueue, so next/previous/autoplay operate on one clear source of truth.
func getAdjacentMedia(screen *FyneScreen, delta int) (string, string, error) {
	return getAdjacentQueuedMedia(screen, delta, false)
}

func getAdjacentQueuedMedia(screen *FyneScreen, delta int, wrap bool) (string, string, error) {
	queue, _ := screen.queueSnapshot()
	if queue == nil || queue.Len() == 0 {
		return "", "", errors.New(lang.L("queue is empty"))
	}

	currentIndex := queue.IndexByPath(screen.mediafile)
	if currentIndex == -1 {
		return "", "", errors.New(lang.L("current media file is not in the queue"))
	}
	queue.SetCurrentIndex(currentIndex)

	nextIndex := queue.AdjacentIndex(delta, screen.SkinNextOnlySameTypes, wrap)
	if nextIndex == -1 {
		if delta < 0 {
			return "", "", errNoPreviousQueueMedia
		}

		return "", "", errNoNextQueueMedia
	}

	item, _ := queue.Item(nextIndex)
	return item.BaseName(), item.Path(), nil
}

func isTraversalBoundaryError(err error) bool {
	return errors.Is(err, errNoNextQueueMedia) ||
		errors.Is(err, errNoPreviousQueueMedia)
}

func getNextAutoPlayMediaOrError(screen *FyneScreen) (string, string, error) {
	return getAdjacentQueuedMedia(screen, 1, true)
}

func autoSelectNextSubs(v string, screen *FyneScreen) {
	name, path := getNextPossibleSubs(v)
	screen.SubsText.Text = name
	screen.subsfile = path
	fyne.Do(func() {
		screen.SubsText.Refresh()
	})
}

func getNextPossibleSubs(v string) (string, string) {
	var name, path string

	possibleSub := v[0:len(v)-
		len(filepath.Ext(v))] + ".srt"

	if _, err := os.Stat(possibleSub); err == nil {
		name = filepath.Base(possibleSub)
		path = possibleSub
	}

	return name, path
}

func setPlayPauseView(s string, screen *FyneScreen) {
	if screen.cancelEnablePlay != nil {
		screen.cancelEnablePlay()
	}

	if screen.renderGate.remoteLeaseHeld() {
		// Renderer controls stay locked while the remote session runs; the
		// lease release recomputes availability.
		return
	}

	fyne.Do(func() {
		// Check if we are casting an image
		isImage := false
		screen.mu.RLock()
		if strings.HasPrefix(screen.castingMediaType, "image/") {
			isImage = true
		}
		screen.mu.RUnlock()

		if isImage {
			screen.PlayPause.Disable()
			screen.PlayPause.SetIcon(theme.FileImageIcon())
			screen.PlayPause.SetText(lang.L("Image Casting") + "  ")
		} else {
			state := screen.getScreenState()
			if state == "Playing" || state == "Paused" {
				screen.PlayPause.Enable()
				switch s {
				case "Play":
					screen.PlayPause.SetText(lang.L("Play") + "  ")
					screen.PlayPause.SetIcon(theme.MediaPlayIcon())
				case "Pause":
					screen.PlayPause.SetText(lang.L("Pause") + "  ")
					screen.PlayPause.SetIcon(theme.MediaPauseIcon())
				}
			} else {
				// Stopped or initial state
				screen.PlayPause.Enable()

				if screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked && screen.selectedDeviceType == devices.DeviceTypeChromecast {
					screen.PlayPause.SetText(lang.L("Start RTMP Session") + "  ")
				} else {
					screen.PlayPause.SetText(lang.L("Cast") + "  ")
				}
				screen.PlayPause.SetIcon(theme.MediaPlayIcon())
			}
		}
		screen.PlayPause.Refresh()
		screen.refreshTraversalControls()
	})
}

func setMuteUnmuteView(muted bool, screen *FyneScreen) {
	screen.mu.Lock()
	screen.muted = muted
	screen.mu.Unlock()

	fyne.Do(func() {
		screen.MuteUnmute.SetIcon(theme.VolumeMuteIcon())
		if muted {
			screen.MuteUnmute.Importance = widget.DangerImportance
		} else {
			screen.MuteUnmute.Importance = widget.LowImportance
		}
		screen.MuteUnmute.Refresh()
	})
}

func (p *FyneScreen) isMuted() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.muted
}

// updateScreenState updates the screen state based on
// the emitted messages. The State variable is used across
// the GUI interface to control certain flows.
func (p *FyneScreen) updateScreenState(a string) {
	p.mu.Lock()
	p.State = a
	p.mu.Unlock()

	fyne.Do(func() {
		if p.DeviceList != nil {
			p.DeviceList.Refresh()
		}
		p.updateActiveDeviceView()
	})
}

func (p *FyneScreen) setActiveDevice(device devType) {
	p.mu.Lock()
	p.activeDevice = device
	p.mu.Unlock()

	fyne.Do(func() {
		if p.DeviceList != nil {
			p.DeviceList.Refresh()
		}
		p.updateActiveDeviceView()
	})
}

func (p *FyneScreen) clearActiveDevice() {
	p.setActiveDevice(devType{})
}

func (p *FyneScreen) getActiveDevice() devType {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.activeDevice
}

func (p *FyneScreen) updateActiveDeviceView() {
	if p.ActiveDeviceCard == nil || p.ActiveDeviceLabel == nil {
		return
	}

	if p.renderGate.remoteLeaseHeld() {
		p.ActiveDeviceCard.Hide()
		return
	}

	p.ActiveDeviceLabel.Importance = widget.MediumImportance
	p.ActiveDeviceLabel.Refresh()
	if p.ActiveDeviceIcon != nil {
		p.ActiveDeviceIcon.SetResource(theme.MediaPlayIcon())
	}

	state := p.getScreenState()
	isActivePlayback := state == "Playing" || state == "Paused"

	if !isActivePlayback {
		p.ActiveDeviceCard.Hide()
		return
	}

	activeDevice := p.getActiveDevice()
	if activeDevice.name != "" {
		p.ActiveDeviceLabel.SetText(activeDevice.name)
		p.ActiveDeviceCard.Show()
	} else {
		p.ActiveDeviceCard.Hide()
	}
}

// getScreenState returns the current screen state
func (p *FyneScreen) getScreenState() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.State
}

func (p *FyneScreen) nextChromecastActionID() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chromecastActionID++
	return p.chromecastActionID
}

func (p *FyneScreen) isChromecastActionCurrent(actionID uint64) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.chromecastActionID == actionID
}

func chromecastDeviceHost(device devType) string {
	if device.deviceType != devices.DeviceTypeChromecast || device.addr == "" {
		return ""
	}

	u, err := url.Parse(device.addr)
	if err != nil {
		return ""
	}

	return u.Hostname()
}

func chromecastClientOwnsDevice(client *castprotocol.CastClient, device devType) bool {
	if client == nil {
		return false
	}

	deviceHost := chromecastDeviceHost(device)
	if deviceHost == "" {
		return false
	}

	return strings.EqualFold(client.Host(), deviceHost)
}

func (p *FyneScreen) chromecastSessionClient() *castprotocol.CastClient {
	client := p.chromecastClient
	if client == nil || !client.IsConnected() {
		return nil
	}

	if !chromecastClientOwnsDevice(client, p.getActiveDevice()) {
		return nil
	}

	return client
}

func (p *FyneScreen) activeChromecastPlaybackClient() *castprotocol.CastClient {
	switch p.getScreenState() {
	case "Playing", "Paused":
		return p.chromecastSessionClient()
	default:
		return nil
	}
}

func (p *FyneScreen) reusableChromecastClientForSelectedDevice() *castprotocol.CastClient {
	return p.reusableChromecastClientForDevice(p.selectedDevice)
}

func (p *FyneScreen) reusableChromecastClientForDevice(device devType) *castprotocol.CastClient {
	client := p.chromecastClient
	if client == nil || !client.IsConnected() {
		return nil
	}

	if !chromecastClientOwnsDevice(client, device) {
		return nil
	}

	return client
}

// checkChromecastCompatibility checks if loaded media needs transcoding for Chromecast.
// Auto-enables transcode checkbox if media is incompatible and FFmpeg is available.
// Only auto-enables once per file - tracks checked file to respect user's manual disable.
func (p *FyneScreen) checkChromecastCompatibility() {
	if p.selectedDeviceType != devices.DeviceTypeChromecast {
		return
	}
	if p.mediafile == "" {
		return
	}
	// Skip if we've already auto-checked this file (prevents re-enabling after user disables)
	if p.chromecastCheckedFile == p.mediafile {
		return
	}
	if err := p.ffmpegStatus(); err != nil {
		return // Can't transcode anyway
	}

	// Only auto-enable transcoding for video files
	// Images and audio are natively supported by Chromecast
	ext := strings.ToLower(filepath.Ext(p.mediafile))
	if !slices.Contains(p.videoFormats, ext) {
		return // Not a video file, no need to check compatibility
	}

	info, err := utils.GetMediaCodecInfo(p.ffmpegPath, p.mediafile)
	if err != nil {
		return // Can't determine, let user decide
	}

	// Mark this file as checked (even if compatible) to avoid rechecking
	p.chromecastCheckedFile = p.mediafile

	if !utils.IsChromecastCompatible(info) {
		fyne.Do(func() {
			p.TranscodeCheckBox.SetChecked(true)
		})
		p.Transcode = true
	}
}

// NewFyneScreen creates and initializes a new FyneScreen instance with the provided version string.
func NewFyneScreen(version string, crash *crashlog.Session) *FyneScreen {
	go2tv := app.NewWithID("app.go2tv.go2tv")

	// Hack. Ongoing discussion in https://github.com/fyne-io/fyne/issues/5333
	var content []byte
	switch go2tv.Preferences().String("Language") {
	case "中文(简体)":
		content, _ = translations.ReadFile("translations/zh.json")
	case "English":
		content, _ = translations.ReadFile("translations/en.json")
	}

	if content != nil {
		name := lang.SystemLocale().LanguageString()
		_ = lang.AddTranslations(fyne.NewStaticResource(name+".json", content))
	} else {
		_ = lang.AddTranslationsFS(translations, "translations")
	}

	go2tv.SetIcon(fyne.NewStaticResource("icon", go2TVIcon512))

	w := go2tv.NewWindow("Go2TV")
	currentDir, err := os.Getwd()
	if err != nil {
		currentDir = ""
	}

	dw := newDebugWriter(runtimeDebugRingSize)
	discoveryDebug := newDebugWriter(discoveryDebugRingSize)
	devices.SetDiscoveryLogOutput(discoveryDebug)

	ffmpegPath := func() string {
		if go2tv.Preferences().String("ffmpeg") != "" {
			path, err := utils.ResolveFFmpegPath(go2tv.Preferences().String("ffmpeg"))
			if err == nil {
				return path
			}
		}

		path, _ := utils.ResolveFFmpegPath("")
		return path
	}()

	return &FyneScreen{
		Current:            w,
		currentmfolder:     currentDir,
		ffmpegPath:         ffmpegPath,
		mediaFormats:       mediamodel.AllMediaExtensions(),
		imageFormats:       mediamodel.ImageExtensions(),
		videoFormats:       mediamodel.VideoExtensions(),
		audioFormats:       mediamodel.AudioExtensions(),
		version:            version,
		Debug:              dw,
		DiscoveryDebug:     discoveryDebug,
		Crash:              crash,
		PendingCrashPath:   crashPath(crash),
		queueSelectedIndex: -1,
		lastQueueTapIndex:  -1,
		remoteSession:      newRemoteSessionManager(),
		shutdownDone:       make(chan struct{}),
	}
}

func crashPath(crash *crashlog.Session) string {
	if crash == nil {
		return ""
	}

	return crash.PreviousCrashPath()
}

func onDropFiles(screen *FyneScreen) func(p fyne.Position, u []fyne.URI) {
	return func(p fyne.Position, u []fyne.URI) {
		handleDroppedFiles(screen, droppedMediaModeReplace, u)
	}
}

func handleDroppedFiles(screen *FyneScreen, mode droppedMediaMode, uris []fyne.URI) {
	if screen.renderGate.remoteLeaseHeld() {
		return
	}

	mfiles, sfiles := splitDroppedFiles(screen, uris)

	if mode == droppedMediaModeReplace && len(sfiles) > 0 {
		screen.CustomSubsCheck.SetChecked(true)
		selectSubsFile(screen, sfiles[0])
	}

	if len(mfiles) == 0 {
		return
	}

	if err := screen.droppedMediaBlockedErrorForMode(mode); err != nil {
		check(screen, err)
		return
	}

	paths := make([]string, 0, len(mfiles))
	for _, mediaURI := range mfiles {
		paths = append(paths, mediaURI.Path())
	}

	go func() {
		var err error
		switch mode {
		case droppedMediaModeAppend:
			err = appendMediaPaths(screen, paths)
		default:
			err = selectMediaPaths(screen, paths)
		}

		check(screen, err)
	}()
}

func splitDroppedFiles(screen *FyneScreen, uris []fyne.URI) ([]fyne.URI, []fyne.URI) {
	var mfiles, sfiles []fyne.URI

out:
	for _, f := range uris {
		if strings.HasSuffix(strings.ToUpper(f.Name()), ".SRT") {
			sfiles = append(sfiles, f)
			continue
		}

		for _, s := range screen.mediaFormats {
			if strings.HasSuffix(strings.ToUpper(f.Name()), strings.ToUpper(s)) {
				mfiles = append(mfiles, f)
				continue out
			}
		}
	}

	return mfiles, sfiles
}

func (screen *FyneScreen) droppedMediaBlockedErrorForMode(mode droppedMediaMode) error {
	switch {
	case screen.Screencast:
		return errors.New(lang.L("disable Cast Desktop before dropping files"))
	case screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked:
		return errors.New(lang.L("disable RTMP server before dropping files"))
	case mode == droppedMediaModeAppend && screen.ExternalMediaURL != nil && screen.ExternalMediaURL.Checked:
		return errors.New(lang.L("disable Media from URL before adding queue files"))
	default:
		return nil
	}
}
