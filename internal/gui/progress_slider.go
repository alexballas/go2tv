package gui

import (
	"math"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

type tappedSlider struct {
	*widget.Slider
	screen   *FyneScreen
	duration float64
	payload  *soapcalls.TVPayload
	client   *castprotocol.CastClient
	actionID uint64
	seekable bool
}

func newTappableSlider(s *FyneScreen) *tappedSlider {
	slider := &tappedSlider{
		Slider: &widget.Slider{
			Max: 100,
		},
		screen: s,
	}
	slider.ExtendBaseWidget(slider)
	slider.Disable()
	return slider
}

// Enable requests must preserve seek availability, including settings/tab changes.
func (t *tappedSlider) Enable() {
	if t.canSeek() {
		t.Slider.Enable()
	} else {
		t.Slider.Disable()
	}
}

// Called on the UI thread with samples belonging to the current session.
func (t *tappedSlider) setDLNADuration(payload *soapcalls.TVPayload, duration float64) {
	if t.screen == nil || t.screen.tvdata != payload || t.screen.getActiveDevice().deviceType == devices.DeviceTypeChromecast {
		return
	}
	t.payload, t.client, t.duration = payload, nil, duration
	t.Enable()
}

func (t *tappedSlider) setChromecastDuration(client *castprotocol.CastClient, actionID uint64, duration float64, seekable bool) {
	if t.screen == nil || t.screen.chromecastSessionClient() != client || !t.screen.isChromecastActionCurrent(actionID) {
		return
	}
	t.client, t.payload, t.actionID = client, nil, actionID
	t.duration, t.seekable = duration, seekable
	t.Enable()
}

func (t *tappedSlider) seekDuration() (float64, bool) {
	if client := t.screen.activeChromecastPlaybackClient(); client != nil {
		return t.duration, t.client == client && t.screen.isChromecastActionCurrent(t.actionID) && t.seekable && t.duration > 0
	}
	if t.screen.getActiveDevice().deviceType == devices.DeviceTypeChromecast {
		return 0, false
	}
	payload := t.screen.tvdata
	if payload == nil || payload.ControlURL == "" {
		return 0, false
	}
	if payload.Transcode {
		return payload.MediaDuration, payload.MediaDuration > 0
	}
	return t.duration, t.payload == payload && payload.Seekable && t.duration > 0
}

func (t *tappedSlider) Dragged(e *fyne.DragEvent) {
	if t.Disabled() || !t.canSeek() {
		return
	}
	t.Slider.Dragged(e)
	t.screen.sliderActive = true
	duration, _ := t.seekDuration()
	current := int(math.Round(duration * t.Value / t.Max))
	t.screen.CurrentPos.Set(utils.SecondsToClockTime(current))
	t.screen.EndPos.Set(utils.SecondsToClockTime(int(math.Round(duration))))
}

func (t *tappedSlider) DragEnd() {
	if t.Disabled() || !t.canSeek() {
		return
	}
	// This ensures the slider functions correctly by addressing the race condition
	// between the DragEnd action and the auto-refresh action.
	// The auto-refresh action will reset this flag to false after the first iteration.
	t.screen.sliderActive = true

	if t.canSeek() {
		releasePermit, permitted := t.screen.rendererPermit(false)
		if !permitted {
			return
		}
		defer releasePermit()

		// Handle Chromecast seeking
		if client := t.screen.activeChromecastPlaybackClient(); client != nil {
			duration, ok := t.seekDuration()
			if !ok {
				return
			}
			seekPos := int((t.screen.SlideBar.Value / t.screen.SlideBar.Max) * duration)
			// Transcoded seek: use optimized helper that keeps connection open
			// (Chromecast's native Seek() doesn't work on transcoded streams)
			if t.screen.mediaDuration > 0 {
				chromecastTranscodedSeek(t.screen, seekPos)
				return
			}
			// Non-transcoded seek: use Chromecast's native seek
			if err := client.Seek(seekPos); err != nil {
				return
			}
			t.screen.notifyMPRISSeek(seekPos)
			return
		}

		t.seekDLNAAsync()
	}
}

func (t *tappedSlider) Tapped(p *fyne.PointEvent) {
	if t.Disabled() || !t.canSeek() {
		return
	}
	// The auto-refresh action should reset this back to false
	// after the first iterration.
	t.screen.sliderActive = true

	t.Slider.Tapped(p)

	if t.canSeek() {
		releasePermit, permitted := t.screen.rendererPermit(false)
		if !permitted {
			return
		}
		defer releasePermit()

		// Handle Chromecast seeking
		if client := t.screen.activeChromecastPlaybackClient(); client != nil {
			duration, ok := t.seekDuration()
			if !ok {
				return
			}

			seekPos := int((t.screen.SlideBar.Value / t.screen.SlideBar.Max) * duration)

			// Update time labels immediately for visual feedback (like DLNA)
			current := utils.SecondsToClockTime(seekPos)
			total := utils.SecondsToClockTime(int(duration))
			fyne.Do(func() {
				t.screen.CurrentPos.Set(current)
				t.screen.EndPos.Set(total)
			})

			// Transcoded seek: use optimized helper that keeps connection open
			if t.screen.mediaDuration > 0 {
				chromecastTranscodedSeek(t.screen, seekPos)
				return
			}

			// Non-transcoded seek: use Chromecast's native seek
			if err := client.Seek(seekPos); err != nil {
				return
			}
			t.screen.notifyMPRISSeek(seekPos)

			return
		}

		t.seekDLNAAsync()
	}
}

func (t *tappedSlider) canSeek() bool {
	if t.screen == nil || !t.screen.progressSeekingAllowed() {
		return false
	}
	if _, ok := t.seekDuration(); !ok {
		return false
	}
	switch t.screen.getScreenState() {
	case "Playing", "Paused":
		return true
	}

	// A renderer may remain TRANSITIONING while it buffers a live transcode,
	// leaving the GUI state stale even though the media session is active.
	// Restart-based seeking does not require the renderer's native seek state.
	tvdata := t.screen.tvdata
	return tvdata != nil && tvdata.ControlURL != "" && tvdata.Transcode && t.screen.getActiveDevice().addr != ""
}

func (t *tappedSlider) seekDLNAAsync() {
	if t.screen.tvdata == nil {
		return
	}

	tvdata := t.screen.tvdata
	sliderValue := t.screen.SlideBar.Value
	sliderMax := t.screen.SlideBar.Max
	if sliderMax == 0 {
		return
	}
	isTranscode := tvdata.Transcode

	releasePermit, permitted := t.screen.rendererPermit(false)
	if !permitted {
		return
	}

	finishSeek := t.screen.beginDLNASeek(tvdata)
	go func() {
		defer releasePermit()
		defer finishSeek()
		total, end := 0, ""
		if known, ok := t.screen.dlnaKnownDuration(tvdata); ok && !isTranscode {
			total, end = known, utils.SecondsToClockTime(known)
		} else {
			var err error
			if total, end, err = dlnaSeekTimeline(tvdata); err != nil {
				return
			}
		}

		cur := (float64(total) * sliderValue) / sliderMax
		roundedInt := int(math.Round(cur))

		reltime := utils.SecondsToClockTime(roundedInt)

		fyne.Do(func() {
			t.screen.CurrentPos.Set(reltime)
			t.screen.EndPos.Set(end)
		})

		if isTranscode {
			// playAction reads these from its own goroutine, so set them
			// here instead of inside the queued fyne.Do above.
			t.screen.ffmpegSeek = roundedInt
			t.screen.dlnaSeekRestart = true
			tvdata.Log().Debug("", "Method", "DLNATranscodeSeek", "Action", "Restart", "Offset", roundedInt)

			// Transcoded DLNA uses a server restart at the selected offset.
			stopActionSync(t.screen)
			playAction(t.screen)
			return
		}

		if err := tvdata.SeekSoapCall(reltime); err != nil {
			return
		}
		t.screen.noteDLNASeek(tvdata, roundedInt, total)
		t.screen.notifyMPRISSeek(roundedInt)
	}()
}

func sliderUpdate(s *FyneScreen) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		if s.sliderActive {
			s.sliderActive = false
			continue
		}

		state := s.getScreenState()
		tvdata := s.tvdata
		activeTranscode := tvdata != nil && tvdata.Transcode && tvdata.ControlURL != ""
		if activeTranscode && stopFailedTranscode(s, s.httpserver, tvdata, 0) {
			continue
		}

		if (state == "Stopped" || state == "") && s.ffmpegSeek == 0 && !activeTranscode {
			// Don't reset slider for Chromecast - it has its own status watcher
			if s.chromecastSessionClient() != nil {
				continue
			}
			// A monitored DLNA startup is still "Stopped"; the monitor owns progress.
			if tvdata != nil && s.dlnaProgressMonitored(tvdata) {
				continue
			}
			fyne.Do(func() {
				s.SlideBar.Disable()
				s.SlideBar.Slider.SetValue(0)
				s.CurrentPos.Set("00:00:00")
				s.EndPos.Set("00:00:00")
			})
		}

		if state == "Playing" || state == "Paused" || activeTranscode {
			// Skip for Chromecast - it has its own status watcher (chromecastStatusWatcher)
			if s.activeChromecastPlaybackClient() != nil {
				continue
			}

			if tvdata == nil {
				continue
			}

			if s.dlnaProgressMonitored(tvdata) {
				continue
			}

			getPos, err := tvdata.GetPositionInfo()
			if err != nil {
				fyne.Do(func() { s.SlideBar.setDLNADuration(tvdata, 0) })
				continue
			}

			current, total, end, err := dlnaProgressTimeline(tvdata, getPos)
			if err != nil {
				fyne.Do(func() { s.SlideBar.setDLNADuration(tvdata, 0) })
				continue
			}

			valueToSet := float64(current) * s.SlideBar.Max / float64(total)
			payloadSeek := tvdata.FFmpegSeek
			currentClock := utils.SecondsToClockTime(current)
			fyne.Do(func() {
				if s.tvdata != tvdata {
					return
				}
				s.SlideBar.setDLNADuration(tvdata, float64(total))
				if s.tvdata == tvdata && s.ffmpegSeek == payloadSeek {
					s.ffmpegSeek = 0
				}
				s.SlideBar.SetValue(valueToSet)
				s.CurrentPos.Set(currentClock)
				s.EndPos.Set(end)
				s.refreshMPRISProgress()
			})
			s.persistResumeProgress(current, float64(total), false)
		}
	}
}
