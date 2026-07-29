package gui

import (
	"math"
	"sync"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/widget"
	"go2tv.app/go2tv/v2/utils"
)

type tappedSlider struct {
	*widget.Slider
	screen *FyneScreen
	end    string
	ccDur  float64
	mu     sync.Mutex
}

func newTappableSlider(s *FyneScreen) *tappedSlider {
	slider := &tappedSlider{
		Slider: &widget.Slider{
			Max: 100,
		},
		screen: s,
	}
	slider.ExtendBaseWidget(slider)
	return slider
}

func (t *tappedSlider) chromecastSeekDuration() (float64, bool) {
	if t.screen.mediaDuration > 0 {
		return t.screen.mediaDuration, true
	}

	t.mu.Lock()
	cached := t.ccDur
	t.mu.Unlock()
	if cached > 0 {
		return cached, true
	}

	client := t.screen.activeChromecastPlaybackClient()
	if client == nil {
		return 0, false
	}

	status, err := client.GetStatus()
	if err != nil || status.Duration <= 0 {
		return 0, false
	}

	duration := float64(status.Duration)

	t.mu.Lock()
	t.ccDur = duration
	t.mu.Unlock()

	// Cache briefly while dragging to avoid hammering GetStatus.
	go func() {
		time.Sleep(time.Second)
		t.mu.Lock()
		t.ccDur = 0
		t.mu.Unlock()
	}()

	return duration, true
}

func (t *tappedSlider) Dragged(e *fyne.DragEvent) {
	t.Slider.Dragged(e)
	t.screen.sliderActive = true

	if duration, ok := t.chromecastSeekDuration(); ok {
		cur := (duration * t.Slider.Value) / t.Slider.Max
		reltime := utils.SecondsToClockTime(int(cur))
		total := utils.SecondsToClockTime(int(duration))
		t.screen.CurrentPos.Set(reltime)
		t.screen.EndPos.Set(total)
		return
	}

	// DLNA: Get position from device
	t.mu.Lock()
	cachedEnd := t.end
	t.mu.Unlock()

	if cachedEnd == "" {
		_, end, err := dlnaSeekTimeline(t.screen.tvdata)
		if err != nil {
			return
		}

		t.mu.Lock()
		t.end = end
		cachedEnd = t.end
		t.mu.Unlock()

		// poor man's caching to reduce the amount of
		// GetPositionInfo calls.
		go func() {
			time.Sleep(time.Second)
			t.mu.Lock()
			t.end = ""
			t.mu.Unlock()
		}()
	}

	total, err := utils.ClockTimeToSeconds(cachedEnd)
	if err != nil {
		return
	}

	cur := (float64(total) * t.Slider.Value) / t.Slider.Max
	roundedInt := int(math.Round(cur))

	reltime := utils.SecondsToClockTime(roundedInt)

	end, err := utils.FormatClockTime(cachedEnd)
	if err != nil {
		return
	}

	t.screen.EndPos.Set(end)
	t.screen.CurrentPos.Set(reltime)
}

func (t *tappedSlider) DragEnd() {
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
			duration, ok := t.chromecastSeekDuration()
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
			return
		}

		t.seekDLNAAsync()
	}
}

func (t *tappedSlider) Tapped(p *fyne.PointEvent) {
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
			duration, ok := t.chromecastSeekDuration()
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

			return
		}

		t.seekDLNAAsync()
	}
}

func (t *tappedSlider) canSeek() bool {
	switch t.screen.getScreenState() {
	case "Playing", "Paused":
		return true
	}

	// A renderer may remain TRANSITIONING while it buffers a live transcode,
	// leaving the GUI state stale even though the media session is active.
	// Restart-based seeking does not require the renderer's native seek state.
	tvdata := t.screen.tvdata
	return tvdata != nil && tvdata.ControlURL != "" && tvdata.Transcode
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

	go func() {
		defer releasePermit()
		total, end, err := dlnaSeekTimeline(tvdata)
		if err != nil {
			return
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

		_ = tvdata.SeekSoapCall(reltime)
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

		if (state == "Stopped" || state == "") && s.ffmpegSeek == 0 && !activeTranscode {
			// Don't reset slider for Chromecast - it has its own status watcher
			if s.chromecastSessionClient() != nil {
				continue
			}
			fyne.Do(func() {
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

			getPos, err := tvdata.GetPositionInfo()
			if err != nil {
				continue
			}

			current, total, end, err := dlnaProgressTimeline(tvdata, getPos)
			if err != nil {
				continue
			}

			valueToSet := float64(current) * s.SlideBar.Max / float64(total)
			payloadSeek := tvdata.FFmpegSeek
			currentClock := utils.SecondsToClockTime(current)
			fyne.Do(func() {
				if s.tvdata == tvdata && s.ffmpegSeek == payloadSeek {
					s.ffmpegSeek = 0
				}
				s.SlideBar.SetValue(valueToSet)
				s.CurrentPos.Set(currentClock)
				s.EndPos.Set(end)
			})
			s.persistResumeProgress(current, float64(total), false)
		}
	}
}
