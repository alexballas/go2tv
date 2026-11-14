package gui

import (
	"context"
	"slices"
	"sync"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/utils"
)

// audioTrackState holds the selected media's audio streams and the user's
// choice. The choice applies when casting starts; a cast records the
// effective session values so restarts (seeks) keep the same track.
type audioTrackState struct {
	mu      sync.Mutex
	media   string // identity of the probed media and FFmpeg path
	tracks  []utils.AudioTrack
	chosen  int // Number; zero follows the source default
	cancel  context.CancelFunc
	syncing bool
	// autoTranscode records that choosing a track turned Transcode on, so
	// returning to the default track turns it back off.
	autoTranscode bool
	togglingCheck bool

	sessionTrack int
}

// audioPicker groups the per-platform widgets. objects are shown only when
// the media offers a choice; hint is an optional tooltip setter.
type audioPicker struct {
	selectWidget *widget.Select
	objects      []fyne.CanvasObject
	hint         func(string)
}

// audioCast resolves a cast of media: a non-default track is transcoded
// through FFmpeg. It records the track for restarts of the same session.
func (s *FyneScreen) audioCast(media string, transcode bool) (track int, transcodeOut bool) {
	s.audio.mu.Lock()
	defer s.audio.mu.Unlock()
	if media != "" && audioTrackKey(media, s.ffmpegPath) == s.audio.media {
		track = utils.AudioTrackOverride(s.audio.tracks, s.audio.chosen)
	}
	s.audio.sessionTrack = track
	return track, transcode || track > 0
}

// castAudio returns the audio track of the current session.
func (s *FyneScreen) castAudio() int {
	s.audio.mu.Lock()
	defer s.audio.mu.Unlock()
	return s.audio.sessionTrack
}

// clearCastAudio drops session audio for casts that never select a track.
func (s *FyneScreen) clearCastAudio() {
	s.audio.mu.Lock()
	s.audio.sessionTrack = 0
	s.audio.mu.Unlock()
}

// refreshAudioPicker syncs the choices with the selected media, probing new
// media in the background. Called on the UI thread.
func (s *FyneScreen) refreshAudioPicker() {
	if s.audioPicker.selectWidget == nil {
		return
	}
	media, probe := s.audioTrackMedia()
	// Tracks probed with another FFmpeg are stale: the choice must never
	// outlive the binary that would play it.
	if media != "" {
		media = audioTrackKey(media, s.ffmpegPath)
	}
	s.audio.mu.Lock()
	if media != s.audio.media {
		if s.audio.cancel != nil {
			s.audio.cancel()
		}
		revert := s.audio.autoTranscode
		s.audio.media, s.audio.tracks, s.audio.chosen, s.audio.cancel, s.audio.autoTranscode = media, nil, 0, nil, false
		if revert {
			s.audio.mu.Unlock()
			s.setTranscodeForAudio(false)
			s.audio.mu.Lock()
		}
		if media != "" && probe != nil {
			ctx, cancel, done, ok := s.workers.begin(s.torrent.ctx)
			if !ok {
				s.audio.mu.Unlock()
				return
			}
			s.audio.cancel = cancel
			go func() {
				defer done()
				tracks, err := probe(ctx)
				if err != nil || len(tracks) <= 1 {
					cancel()
					return
				}
				fyne.Do(func() {
					defer cancel()
					s.audio.mu.Lock()
					current := s.audio.media == media && ctx.Err() == nil
					if current {
						s.audio.tracks = tracks
					}
					s.audio.mu.Unlock()
					if current {
						s.refreshAudioPicker()
					}
				})
			}()
		}
	}
	tracks, chosen := slices.Clone(s.audio.tracks), s.audio.chosen
	s.audio.mu.Unlock()
	s.renderAudioPicker(tracks, chosen)
}

func (s *FyneScreen) renderAudioPicker(tracks []utils.AudioTrack, chosen int) {
	picker := s.audioPicker
	visible := len(tracks) > 1
	for _, object := range picker.objects {
		if object.Visible() != visible {
			if visible {
				object.Show()
			} else {
				object.Hide()
			}
		}
	}
	if !visible {
		return
	}
	defaultTrack := utils.DefaultAudioTrack(tracks)
	if chosen == 0 {
		chosen = defaultTrack
	}
	options := make([]string, 0, len(tracks))
	selected := 0
	for i, track := range tracks {
		label := track.Label()
		if track.Number == defaultTrack {
			label += " (" + lang.L("default") + ")"
		}
		if track.Number == chosen {
			selected = i
		}
		options = append(options, label)
	}
	s.audio.mu.Lock()
	s.audio.syncing = true
	s.audio.mu.Unlock()
	if !slices.Equal(picker.selectWidget.Options, options) {
		picker.selectWidget.SetOptions(options)
	}
	picker.selectWidget.SetSelectedIndex(selected)
	s.audio.mu.Lock()
	s.audio.syncing = false
	s.audio.mu.Unlock()

	// The track is fixed once casting starts.
	switch s.getScreenState() {
	case "Playing", "Paused":
		picker.selectWidget.Disable()
		if picker.hint != nil {
			picker.hint(lang.L("Stop playback to change the audio track"))
		}
	default:
		picker.selectWidget.Enable()
		if picker.hint != nil {
			picker.hint(lang.L("Other tracks are transcoded with FFmpeg"))
		}
	}
}

// chooseAudioTrack records a picker selection by option index. Another
// track needs FFmpeg, so it turns Transcode on; returning to the default
// undoes that.
func (s *FyneScreen) chooseAudioTrack(index int) {
	s.audio.mu.Lock()
	if s.audio.syncing || index < 0 || index >= len(s.audio.tracks) {
		s.audio.mu.Unlock()
		return
	}
	s.audio.chosen = s.audio.tracks[index].Number
	override := utils.AudioTrackOverride(s.audio.tracks, s.audio.chosen) > 0
	check := s.TranscodeCheckBox
	var set, value bool
	switch {
	case override && check != nil && !check.Checked:
		s.audio.autoTranscode, set, value = true, true, true
	case !override && s.audio.autoTranscode:
		s.audio.autoTranscode, set = false, true
	}
	s.audio.mu.Unlock()
	if set {
		s.setTranscodeForAudio(value)
	}
}

// audioTranscodeToggled follows user changes to Transcode: turning it off
// returns to the source's default track. Called from the toggle handler.
func (s *FyneScreen) audioTranscodeToggled(on bool) {
	s.audio.mu.Lock()
	if s.audio.togglingCheck {
		s.audio.mu.Unlock()
		return
	}
	s.audio.autoTranscode = false
	reset := !on && utils.AudioTrackOverride(s.audio.tracks, s.audio.chosen) > 0
	if reset {
		s.audio.chosen = 0
	}
	s.audio.mu.Unlock()
	if reset {
		s.refreshAudioPicker()
	}
}

// setTranscodeForAudio changes Transcode on behalf of the audio picker.
func (s *FyneScreen) setTranscodeForAudio(on bool) {
	check := s.TranscodeCheckBox
	if check == nil || check.Checked == on {
		return
	}
	s.audio.mu.Lock()
	s.audio.togglingCheck = true
	s.audio.mu.Unlock()
	check.SetChecked(on)
	s.audio.mu.Lock()
	s.audio.togglingCheck = false
	s.audio.mu.Unlock()
	if !on {
		s.audioTranscodeReverted()
	}
}

func audioTrackKey(media, ffmpeg string) string {
	return media + "\x00" + ffmpeg
}
