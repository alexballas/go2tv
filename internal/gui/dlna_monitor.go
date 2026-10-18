//go:build !(android || ios)

package gui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexballas/refyne/v2"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

var errDLNAGaplessUnavailable = errors.New("renderer gapless handoff unavailable")

const (
	// Settle time between a renderer-confirmed EOF and loading the next item.
	dlnaEOFSettleDelay = time.Second
	// Seconds a native seek may land past its target (next keyframe).
	dlnaSeekLanding = 1
)

// Each server owns its callback sink. Old callbacks cannot mutate a replacement
// session, and boundary STOPPED events leave its media routes alive.
type dlnaPlaybackSession struct {
	*FyneScreen
	ctx                 context.Context
	server              *httphandlers.HTTPserver
	subscriptionPayload *soapcalls.TVPayload
	current             atomic.Pointer[soapcalls.TVPayload]
	queued              atomic.Pointer[soapcalls.TVPayload]
	queueing            atomic.Bool
	finished            atomic.Bool
	rendererStopped     atomic.Bool
	unsupported         atomic.Bool
	callbacks           chan playback.DLNACallbackEvent
	monitorMu           sync.Mutex
	monitorCancel       context.CancelFunc
	monitorEpoch        atomic.Uint64
	stopGate            playback.DLNAStopGate
	tailMu              sync.Mutex
	tail                dlnaTail
	queueAttempt        atomic.Pointer[soapcalls.TVPayload]
}

// claimQueue reports whether the caller owns queueing payload's successor.
// One attempt per item keeps start, promotion and recovery from racing.
func (s *dlnaPlaybackSession) claimQueue(payload *soapcalls.TVPayload) bool {
	for {
		previous := s.queueAttempt.Load()
		if previous == payload {
			return false
		}
		if s.queueAttempt.CompareAndSwap(previous, payload) {
			return true
		}
	}
}

// Called on the UI thread. Covers starts whose one-shot queue attempt never
// ran, e.g. Auto-play or Gapless enabled while the renderer was still loading.
func (s *dlnaPlaybackSession) ensureGaplessQueue(current *soapcalls.TVPayload) {
	if s.queued.Load() != nil || s.queueing.Load() || s.NextMediaCheck == nil || !s.NextMediaCheck.Checked ||
		strings.HasPrefix(current.MediaType, "image/") ||
		fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled") != "Enabled" || !s.claimQueue(current) {
		return
	}
	go func() {
		if _, err := queueNext(s.FyneScreen, false); err != nil && !errors.Is(err, context.Canceled) {
			current.Log().Debug("gapless queue failed; ordinary autoplay remains enabled", "error", err)
		}
	}()
}

// dlnaTail is the last known position of a payload. A monitor restarted after
// seek or pause starts from it instead of asking a renderer near its end.
type dlnaTail struct {
	payload            *soapcalls.TVPayload
	position, duration int
}

func (s *dlnaPlaybackSession) noteTail(payload *soapcalls.TVPayload, position, duration int) {
	s.tailMu.Lock()
	defer s.tailMu.Unlock()
	if duration <= 0 {
		if s.tail.payload != payload {
			return
		}
		duration = s.tail.duration
	}
	s.tail = dlnaTail{payload: payload, position: position, duration: duration}
}

// clearTail forgets payload's tail once its duration is gone (live stream).
func (s *dlnaPlaybackSession) clearTail(payload *soapcalls.TVPayload) {
	s.tailMu.Lock()
	defer s.tailMu.Unlock()
	if s.tail.payload == payload {
		s.tail = dlnaTail{}
	}
}

func (s *dlnaPlaybackSession) tailFor(payload *soapcalls.TVPayload) (int, int, bool) {
	s.tailMu.Lock()
	defer s.tailMu.Unlock()
	if s.tail.payload != payload || s.tail.duration <= 0 {
		return 0, 0, false
	}
	return s.tail.position, s.tail.duration, true
}

func (s *FyneScreen) currentDLNASession(payload *soapcalls.TVPayload) *dlnaPlaybackSession {
	s.dlnaQueueMu.Lock()
	session := s.dlnaMonitor
	s.dlnaQueueMu.Unlock()
	if session == nil || session.current.Load() != payload {
		return nil
	}
	return session
}

// noteDLNASeek records a native seek target; duration <= 0 keeps the known one.
// Samsung resumes on the next keyframe, measured ~1.5s past the target, so the
// seed leans late: an early tail guard is harmless, a late one freezes the TV.
func (s *FyneScreen) noteDLNASeek(payload *soapcalls.TVPayload, target, duration int) {
	if session := s.currentDLNASession(payload); session != nil {
		session.noteTail(payload, target+dlnaSeekLanding, duration)
	}
}

// dlnaKnownDuration avoids a GetPositionInfo, which can freeze renderers near EOF.
func (s *FyneScreen) dlnaKnownDuration(payload *soapcalls.TVPayload) (int, bool) {
	session := s.currentDLNASession(payload)
	if session == nil {
		return 0, false
	}
	_, duration, ok := session.tailFor(payload)
	return duration, ok
}

func (s *FyneScreen) prepareDLNAMonitor(ctx context.Context, payload *soapcalls.TVPayload) *dlnaPlaybackSession {
	if s.Screencast || strings.HasPrefix(payload.MediaType, "image/") {
		return nil
	}
	session := &dlnaPlaybackSession{
		FyneScreen: s, ctx: ctx, server: s.httpserver, subscriptionPayload: payload,
		callbacks: make(chan playback.DLNACallbackEvent, 32),
	}
	session.current.Store(payload)
	s.dlnaQueueMu.Lock()
	s.dlnaMonitor = session
	s.dlnaQueueMu.Unlock()
	return session
}

func (s *FyneScreen) cancelDLNAFollowup() uint64 {
	s.dlnaQueueMu.Lock()
	if s.dlnaFollowupCancel != nil {
		s.dlnaFollowupCancel()
		s.dlnaFollowupCancel = nil
	}
	id := s.dlnaActionID.Add(1)
	s.dlnaQueueMu.Unlock()
	return id
}

func (s *FyneScreen) dlnaProgressMonitored(payload *soapcalls.TVPayload) bool {
	s.dlnaQueueMu.Lock()
	defer s.dlnaQueueMu.Unlock()
	return s.dlnaMonitor != nil && s.dlnaMonitor.ctx.Err() == nil && s.dlnaMonitor.current.Load() == payload
}

// Suspend completion while a native seek is in flight. The epoch also fences
// terminal events already queued on the UI thread before the user sought back.
func (s *FyneScreen) beginDLNASeek(payload *soapcalls.TVPayload) func() {
	s.cancelDLNAFollowup()
	s.dlnaQueueMu.Lock()
	session := s.dlnaMonitor
	s.dlnaQueueMu.Unlock()
	if session == nil || session.current.Load() != payload {
		return func() {}
	}
	session.monitorMu.Lock()
	session.monitorEpoch.Add(1)
	if session.monitorCancel != nil {
		session.monitorCancel()
	}
	session.monitorMu.Unlock()
	return func() {
		if session.isCurrent() && !session.finished.Load() {
			go session.run()
		}
	}
}

func (s *FyneScreen) waitDLNATeardown(ctx context.Context) error {
	s.dlnaQueueMu.Lock()
	done := s.dlnaTeardownDone
	s.dlnaQueueMu.Unlock()
	if done == nil {
		return ctx.Err()
	}
	select {
	case <-done:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

type dlnaMonitorSink struct {
	session *dlnaPlaybackSession
	epoch   uint64
}

func (sink dlnaMonitorSink) HandleMonitorEvent(_ context.Context, event playback.MonitorEvent) {
	sink.session.handleMonitorEvent(event, sink.epoch)
}

func (s *dlnaPlaybackSession) run() {
	s.monitorMu.Lock()
	if s.monitorCancel != nil {
		s.monitorCancel()
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.monitorCancel = cancel
	epoch := s.monitorEpoch.Add(1)
	s.monitorMu.Unlock()
	defer cancel()
	position, duration, _ := s.tailFor(s.current.Load())
	playback.RunDLNAMonitor(ctx, playback.MonitorConfig{
		Generation: 1, CurrentURI: s.current.Load().MediaURL, Sink: dlnaMonitorSink{s, epoch}, DLNAStopGate: &s.stopGate,
		InitialPosition: position, InitialDuration: duration,
		GaplessActive: func() bool { return s.queued.Load() != nil || s.queueing.Load() },
		QueuedURI: func() string {
			if queued := s.queued.Load(); queued != nil {
				return queued.MediaURL
			}
			return ""
		},
		DLNATimeline: func() (int, int) {
			current := s.current.Load()
			if current.Transcode {
				return current.FFmpegSeek, int(current.MediaDuration)
			}
			return 0, 0
		},
	}, s, s.callbacks)
}

// Never hold a renderer's NOTIFY open: Samsung's UPnP stack serves one request
// at a time, so a blocked callback freezes every control request. A dropped
// state is still observed by the monitor's transport polling.
func (s *dlnaPlaybackSession) HandleCallbackEvent(_ context.Context, event httphandlers.CallbackEvent) {
	select {
	case s.callbacks <- playback.DLNACallbackEvent{Generation: 1, TransportState: event.TransportState}:
	default:
	}
}

// RetryStart re-sends a start the renderer acknowledged but never began.
func (s *dlnaPlaybackSession) RetryStart(ctx context.Context, uri string, reload bool) error {
	payload := s.current.Load()
	if payload.MediaURL != uri {
		return nil
	}
	if reload {
		if err := payload.SetAVTransportURIContext(ctx); err != nil {
			return fmt.Errorf("RetryStart reload: %w", err)
		}
		// SetAVTransportURI drops the renderer's staged next item. The monitor
		// holds the queue lock here, so the queued payload is stable.
		if queued := s.queued.Load(); queued != nil {
			if err := queued.SetNextAVTransportURIContext(ctx, false); err != nil {
				queued.Log().Debug("gapless requeue failed; ordinary autoplay remains enabled", "error", err)
			}
		}
	}
	if err := payload.PlayPauseStopSoapCallContext(ctx, "Play"); err != nil {
		return fmt.Errorf("RetryStart play: %w", err)
	}
	return nil
}

func (s *dlnaPlaybackSession) TryMonitorLock() (func(), bool) {
	if !s.dlnaQueueOperationMu.TryLock() {
		return nil, false
	}
	return s.dlnaQueueOperationMu.Unlock, true
}

func (s *dlnaPlaybackSession) Position(ctx context.Context) (playback.Position, error) {
	info, err := s.current.Load().GetPositionInfoContext(ctx)
	if err != nil {
		return playback.Position{}, err
	}
	position := playback.Position{TrackURI: info.TrackURI}
	position.Current, err = utils.ClockTimeToSeconds(info.RelTime)
	if err != nil {
		return position, err
	}
	// Some live transcodes omit duration. The monitor applies the source's
	// probed duration without losing the valid renderer position.
	position.Duration, _ = utils.ClockTimeToSeconds(info.TrackDuration)
	return position, nil
}

func (s *dlnaPlaybackSession) MediaInfo(ctx context.Context) (string, string, error) {
	info, err := s.current.Load().GetMediaInfoContext(ctx)
	return info.CurrentURI, info.NextURI, err
}

func (s *dlnaPlaybackSession) TransportState(ctx context.Context) (string, error) {
	values, err := s.current.Load().GetTransportInfoContext(ctx)
	if err != nil {
		return "", err
	}
	return values[0], nil
}

func (s *dlnaPlaybackSession) isCurrent() bool {
	s.dlnaQueueMu.Lock()
	defer s.dlnaQueueMu.Unlock()
	return s.dlnaMonitor == s && s.tvdata == s.current.Load() && s.ctx.Err() == nil
}

func (s *dlnaPlaybackSession) handleMonitorEvent(event playback.MonitorEvent, epoch uint64) {
	if s.monitorEpoch.Load() != epoch || !s.isCurrent() || s.finished.Load() {
		return
	}
	if event.Terminal == playback.TerminalFinished {
		fyne.Do(func() { s.complete(event, epoch) })
		return
	}
	if event.Terminal == playback.TerminalError {
		fyne.Do(func() { s.fail(event, epoch) })
		return
	}
	if event.Terminal != "" {
		return
	}
	if event.NextURIObserved && event.NextURI == "NOT_IMPLEMENTED" && !s.unsupported.Swap(true) {
		s.markGaplessUnavailable()
		go func() {
			if _, err := queueNext(s.FyneScreen, true); err != nil {
				s.current.Load().Log().Debug("could not clear unsupported gapless queue", "error", err)
			}
		}()
	}
	if event.GaplessPromoted {
		s.promote(event.GaplessURI, epoch)
		return
	}
	current := s.current.Load()
	switch {
	case event.Duration > 0:
		s.noteTail(current, event.Position, event.Duration)
	case event.PositionValid:
		s.clearTail(current)
	}
	fyne.Do(func() {
		if s.monitorEpoch.Load() != epoch || !s.isCurrent() || s.finished.Load() || s.current.Load() != current {
			return
		}
		switch event.State {
		case "PLAYING":
			s.updateScreenState("Playing")
			setPlayPauseView("Pause", s.FyneScreen)
			s.ensureGaplessQueue(current)
		case "PAUSED":
			s.updateScreenState("Paused")
			setPlayPauseView("Play", s.FyneScreen)
		}
		switch {
		case event.Duration > 0:
			position := min(max(event.Position, 0), event.Duration)
			s.SlideBar.setDLNADuration(current, float64(event.Duration))
			if !s.sliderActive {
				s.SlideBar.SetValue(float64(position) * s.SlideBar.Max / float64(event.Duration))
				s.CurrentPos.Set(utils.SecondsToClockTime(position))
				s.EndPos.Set(utils.SecondsToClockTime(event.Duration))
			}
			s.persistResumeProgress(position, float64(event.Duration), false)
			s.refreshMPRISProgress()
		case event.PositionValid:
			s.SlideBar.setDLNADuration(current, 0)
			if !s.sliderActive {
				s.SlideBar.SetValue(0)
				s.CurrentPos.Set(utils.SecondsToClockTime(max(event.Position, 0)))
				s.EndPos.Set("00:00:00")
			}
			s.refreshMPRISProgress()
		}
	})
}

func (s *dlnaPlaybackSession) promote(uri string, epoch uint64) {
	var old, queued *soapcalls.TVPayload
	queueFollowing := false
	fyne.DoAndWait(func() {
		if s.monitorEpoch.Load() != epoch || !s.isCurrent() || s.finished.Load() {
			return
		}
		queued = s.queued.Load()
		if queued == nil || queued.MediaURL != uri {
			queued = nil
			return
		}
		old = s.current.Load()
		s.dlnaQueueMu.Lock()
		delete(s.dlnaGaplessStrikes, s.subscriptionPayload.ControlURL)
		s.current.Store(queued)
		s.queued.Store(nil)
		s.tvdata = queued
		s.dlnaQueueMu.Unlock()
		s.promoteQueuedArtwork(guiArtworkIdentity(queued.MediaPath), s.server)
		if err := setCurrentMediaPathOnUI(s.FyneScreen, queued.MediaPath); err != nil {
			check(s.FyneScreen, err)
		}
		updateInternalSubsDropdownAsync(s.FyneScreen, queued.MediaPath)
		s.setPlayingMediaPath(queued.MediaPath)
		s.SetMediaType(queued.MediaType)
		s.updateScreenState("Playing")
		setPlayPauseView("Pause", s.FyneScreen)
		s.configureImageAutoSkipTimer(queued.MediaType, queued.MediaPath)
		queueFollowing = s.NextMediaCheck != nil && s.NextMediaCheck.Checked && fyne.CurrentApp().Preferences().StringWithFallback("Gapless", "Disabled") == "Enabled"
	})
	if queued == nil {
		return
	}
	// Preserve a shared URL (e.g. a one-item looping queue).
	for _, pair := range [][2]string{{old.MediaURL, queued.MediaURL}, {old.SubtitlesURL, queued.SubtitlesURL}} {
		if pair[0] != pair[1] {
			if parsed, err := url.Parse(pair[0]); err == nil && parsed.Path != "" {
				s.server.RemoveHandler(parsed.Path)
			}
		}
	}
	if !queueFollowing || !s.claimQueue(queued) {
		return
	}
	go func() {
		if _, err := queueNext(s.FyneScreen, false); err != nil && !errors.Is(err, context.Canceled) {
			s.current.Load().Log().Debug("gapless queue failed; ordinary autoplay remains enabled", "error", err)
		}
	}()
}

// gaplessStrike records a failed handoff and reports whether the renderer
// reached playback.GaplessHandoffStrikes; a successful promotion resets it.
func (s *dlnaPlaybackSession) gaplessStrike() bool {
	s.dlnaQueueMu.Lock()
	defer s.dlnaQueueMu.Unlock()
	if s.dlnaGaplessStrikes == nil {
		s.dlnaGaplessStrikes = make(map[string]int)
	}
	s.dlnaGaplessStrikes[s.subscriptionPayload.ControlURL]++
	strikes := s.dlnaGaplessStrikes[s.subscriptionPayload.ControlURL]
	return strikes >= playback.GaplessHandoffStrikes
}

func (s *dlnaPlaybackSession) markGaplessUnavailable() {
	s.dlnaQueueMu.Lock()
	if s.dlnaGaplessUnavailable == nil {
		s.dlnaGaplessUnavailable = make(map[string]bool)
	}
	s.dlnaGaplessUnavailable[s.subscriptionPayload.ControlURL] = true
	s.dlnaQueueMu.Unlock()
}

// Called on the UI thread. Selection and renderer identity are captured before
// teardown; a replacement session or explicit Stop cancels the follow-up.
func (s *dlnaPlaybackSession) fail(event playback.MonitorEvent, epoch uint64) {
	if s.monitorEpoch.Load() != epoch || !s.isCurrent() || !s.finished.CompareAndSwap(false, true) {
		return
	}
	s.rendererStopped.Store(event.DLNAStopped)
	s.cancelDLNAFollowup()
	stopActionInternal(s.FyneScreen, false)
	if event.Err != nil {
		check(s.FyneScreen, event.Err)
	}
}

func (s *dlnaPlaybackSession) complete(event playback.MonitorEvent, epoch uint64) {
	if s.monitorEpoch.Load() != epoch || !s.isCurrent() || !s.finished.CompareAndSwap(false, true) {
		return
	}
	s.rendererStopped.Store(event.DLNAStopped)
	recovery := event.Recovery
	if recovery != "" {
		s.current.Load().Log().Debug("DLNA playback recovery", "reason", recovery)
	}
	if recovery == playback.RecoveryGaplessHandoffFailed && s.gaplessStrike() {
		s.markGaplessUnavailable()
	}
	target := autoPlayPlaybackTarget(s.FyneScreen)
	mediaPath := s.current.Load().MediaPath
	nextPath := ""
	queue, _ := s.queueSnapshot()
	if queue != nil && queue.IndexByPath(mediaPath) >= 0 {
		switch {
		case s.NextMediaCheck != nil && s.NextMediaCheck.Checked:
			var nextErr error
			_, nextPath, nextErr = getAdjacentQueuedMediaFromPath(s.FyneScreen, mediaPath, 1, true)
			if nextErr != nil {
				s.current.Load().Log().Debug("autoplay successor unavailable", "media", mediaPath, "error", nextErr)
			}
		case s.Medialoop:
			nextPath = mediaPath
		}
	} else if s.Medialoop && s.ExternalMediaURL != nil && s.ExternalMediaURL.Checked {
		nextPath = mediaPath
	}
	preserveSource := s.ExternalMediaURL != nil && s.ExternalMediaURL.Checked && nextPath == mediaPath
	s.FyneScreen.beginDLNAFollowup(target, nextPath, s, preserveSource)
}

// Called on the UI thread: teardown is registered before any replacement can
// start. Stop, manual Play and another skip cancel this follow-up immediately.
func (s *FyneScreen) beginDLNAFollowup(target playbackTarget, nextPath string, expected *dlnaPlaybackSession, preserveSource bool) {
	s.cancelDLNAFollowup()
	followupCtx, cancel := context.WithCancel(context.Background())
	s.dlnaQueueMu.Lock()
	s.dlnaFollowupCancel = cancel
	s.dlnaQueueMu.Unlock()
	var done <-chan struct{}
	if s.stopPlaybackStartup() {
		s.torrent.mu.Lock()
		done = s.torrent.cancelDone
		s.torrent.mu.Unlock()
	} else {
		stopActionInternal(s, false)
		s.dlnaQueueMu.Lock()
		done = s.dlnaTeardownDone
		s.dlnaQueueMu.Unlock()
	}
	settle := expected != nil && expected.finished.Load() && expected.rendererStopped.Load()
	go func() {
		if done != nil {
			select {
			case <-done:
			case <-followupCtx.Done():
				return
			}
		}
		if settle {
			// Samsung renderers acknowledge, then drop, a Play sent right after EOF.
			select {
			case <-time.After(dlnaEOFSettleDelay):
			case <-followupCtx.Done():
				return
			}
		}
		fyne.Do(func() {
			s.dlnaQueueMu.Lock()
			current := s.dlnaMonitor == expected
			s.dlnaQueueMu.Unlock()
			if followupCtx.Err() != nil || !current || nextPath == "" {
				cancel()
				return
			}
			if !preserveSource {
				if err := setCurrentMediaPathOnUI(s, nextPath); err != nil {
					cancel()
					check(s, err)
					return
				}
			}
			if !preserveSource {
				updateInternalSubsDropdownAsync(s, nextPath)
			}
			go playActionOnTargetContext(s, target, followupCtx, false)
		})
	}()
}
