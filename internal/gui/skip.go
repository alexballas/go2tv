package gui

import (
	"math"
	"time"

	"github.com/alexballas/refyne/v2"

	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/utils"
)

// skipSettle groups rapid presses into one seek, so a transcoded stream
// restarts once instead of once per press.
const skipSettle = 450 * time.Millisecond

// skipState is owned by the UI thread.
type skipState struct {
	target  int
	pending bool
	timer   *time.Timer
	buttons []fyne.Disableable
}

// skipAction moves playback relative to its position: back 10s or forward
// 30s per press. The target shows at once; the seek follows when presses
// settle. Called on the UI thread.
func skipAction(s *FyneScreen, delta int) {
	slider := s.SlideBar
	if slider == nil || slider.Disabled() || !slider.canSeek() || slider.Max == 0 {
		return
	}
	duration, ok := slider.seekDuration()
	if !ok {
		return
	}
	base := s.skips.target
	if !s.skips.pending {
		base = int(math.Round(duration * slider.Value / slider.Max))
	}
	target := playback.SkipTarget(base, delta, int(math.Round(duration)))
	s.skips.target, s.skips.pending = target, true
	s.sliderActive = true
	slider.Slider.SetValue(float64(target) * slider.Max / duration)
	_ = s.CurrentPos.Set(utils.SecondsToClockTime(target))
	if s.skips.timer != nil {
		s.skips.timer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(skipSettle, func() {
		fyne.Do(func() {
			if s.skips.timer != timer || !s.skips.pending {
				return
			}
			s.skips.pending, s.skips.timer = false, nil
			s.sliderActive = true
			go seekToPosition(s, s.skips.target)
		})
	})
	s.skips.timer = timer
}

// syncSkipButtons mirrors seek availability onto the skip buttons.
func (s *FyneScreen) syncSkipButtons(enabled bool) {
	for _, button := range s.skips.buttons {
		if enabled {
			button.Enable()
		} else {
			button.Disable()
		}
	}
}

// seekToPosition moves the session to an absolute source position, shared by
// skip buttons and MPRIS. Transcoded streams restart at the target. Call it
// off the UI thread.
func seekToPosition(s *FyneScreen, seconds int) {
	if seconds < 0 {
		return
	}
	release, permitted := s.rendererPermit(false)
	if !permitted {
		return
	}
	defer release()
	if client := s.activeChromecastPlaybackClient(); client != nil {
		if s.mediaDuration > 0 {
			chromecastTranscodedSeek(s, seconds)
			return
		}
		if err := client.Seek(seconds); err == nil {
			s.notifyMPRISSeek(seconds)
		}
		return
	}
	tv := s.tvdata
	if tv == nil || tv.ControlURL == "" {
		return
	}
	finishSeek := s.beginDLNASeek(tv)
	defer finishSeek()
	if tv.Transcode {
		s.ffmpegSeek = seconds
		s.dlnaSeekRestart = true
		tv.Log().Debug("", "Method", "DLNATranscodeSeek", "Action", "Restart", "Offset", seconds)
		stopActionSync(s)
		playAction(s)
		return
	}
	if err := tv.SeekSoapCall(utils.SecondsToClockTime(seconds)); err != nil {
		return
	}
	s.noteDLNASeek(tv, seconds, 0)
	s.notifyMPRISSeek(seconds)
}
