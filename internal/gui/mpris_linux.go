//go:build linux && !android

package gui

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/tunetag"
	"go2tv.app/go2tv/v2/internal/mediaartwork"
	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/mpris"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/utils"
)

type guiMPRIS struct {
	screen  *FyneScreen
	service *mpris.Service
	dir     string
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	last    mpris.Snapshot
	closed  bool
}

func startMPRIS(screen *FyneScreen) mprisBridge {
	ctx, cancel := context.WithCancel(context.Background())
	m := &guiMPRIS{screen: screen, ctx: ctx, cancel: cancel, last: mpris.Snapshot{Volume: 1}}
	service, err := mpris.Start(mpris.Controls{
		Play:      func() { go playAction(screen) },
		Pause:     func() { go playAction(screen) },
		Stop:      func() { go stopAction(screen) },
		Next:      func() { go skipNextAction(screen) },
		Previous:  func() { go skipPreviousAction(screen) },
		Seek:      func(position int64) { go m.seek(position) },
		SetVolume: func(volume float64) { go m.setVolume(volume) },
		SetLoop: func(status string) {
			fyne.Do(func() {
				if screen.LoopSelectedCheck != nil {
					screen.LoopSelectedCheck.SetChecked(status == "Track")
				}
				if screen.NextMediaCheck != nil {
					screen.NextMediaCheck.SetChecked(status == "Playlist")
				}
				m.refresh()
			})
		},
	})
	if err != nil {
		cancel()
		return nil
	} // Session bus is optional on Linux desktops.
	m.service = service
	m.dir, err = mprisArtworkDir()
	if err != nil {
		cancel()
		_ = service.Close()
		return nil
	}
	return m
}

func mprisArtworkDir() (string, error) {
	if os.Getenv("FLATPAK_ID") != mpris.AppID {
		return os.MkdirTemp("", "go2tv-mpris-art-")
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	parent := filepath.Join(cache, "go2tv")
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, "mpris-art-")
}

func (m *guiMPRIS) close() {
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	_ = m.service.Close()
	_ = os.RemoveAll(m.dir)
}

func (m *guiMPRIS) refresh() {
	fyne.Do(func() {
		s := m.screen
		state := s.getScreenState()
		if state != "Playing" && state != "Paused" {
			state = "Stopped"
		}
		path := mprisMediaPath(s)
		current, duration := bindingSeconds(s.CurrentPos), bindingSeconds(s.EndPos)
		active := state == "Playing" || state == "Paused"
		if !active {
			current, duration = 0, 0
		}
		v := mpris.Snapshot{Status: state, Path: path, PositionUS: current * 1_000_000, LengthUS: duration * 1_000_000}
		v.CanPlay = s.getScreenState() != "Waiting" && path != "" &&
			(s.selectedDevice.addr != "" || s.getActiveDevice().addr != "") && !s.renderGate.remoteLeaseHeld()
		v.CanPause = active && !strings.HasPrefix(s.castingMediaType, "image/") && !s.Screencast
		v.CanSeek = active && duration > 0 && !strings.HasPrefix(s.castingMediaType, "image/") && !s.Screencast
		v.CanNext = active && s.SkipNextButton != nil && !s.SkipNextButton.Disabled() && !s.renderGate.remoteLeaseHeld()
		v.CanPrevious = active && s.SkipPreviousButton != nil && !s.SkipPreviousButton.Disabled() && !s.renderGate.remoteLeaseHeld()
		v.LoopStatus = "None"
		if s.Medialoop {
			v.LoopStatus = "Track"
		} else if s.NextMediaCheck != nil && s.NextMediaCheck.Checked {
			v.LoopStatus = "Playlist"
		}
		if path != "" {
			v.Name = filepath.Base(path)
			if strings.Contains(path, "://") {
				v.URL = path
			} else if abs, err := filepath.Abs(path); err == nil {
				v.URL = (&url.URL{Scheme: "file", Path: abs}).String()
			}
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return
		}
		changed := path != m.last.Path
		becameActive := !changed && m.last.Status == "Stopped" && active
		v.Volume = m.last.Volume
		if !changed {
			v.Name, v.Artist, v.Album, v.ArtURL = m.last.Name, m.last.Artist, m.last.Album, m.last.ArtURL
		}
		m.last = v
		m.service.Update(v)
		m.mu.Unlock()
		if changed && path != "" {
			go m.enrich(path, s.ffmpegPath)
		}
		if path != "" && (changed && active || becameActive) {
			go m.readVolume(path)
		}
	})
}

func mprisMediaPath(s *FyneScreen) string {
	if path := s.nowPlayingPath(); path != "" {
		return path
	}
	if s.ExternalMediaURL != nil && s.ExternalMediaURL.Checked {
		if s.MediaText != nil {
			return strings.TrimSpace(s.MediaText.Text)
		}
		return ""
	}
	return s.mediafile
}

func bindingSeconds(value interface{ Get() (string, error) }) int64 {
	if value == nil {
		return 0
	}
	clock, err := value.Get()
	if err != nil {
		return 0
	}
	seconds, err := utils.ClockTimeToSeconds(clock)
	if err != nil || seconds < 0 {
		return 0
	}
	return int64(seconds)
}

func (m *guiMPRIS) enrich(path, ffmpegPath string) {
	v := mpris.Snapshot{Name: filepath.Base(path)}
	if !strings.Contains(path, "://") {
		if tag, err := tunetag.Open(path); err == nil {
			if title := strings.TrimSpace(tag.Title()); title != "" {
				v.Name = title
			}
			v.Artist = tag.Artist()
			v.Album = tag.Album()
		}
		var asset *metadata.ArtworkAsset
		if kind := mediamodel.KindForPath(path); kind == mediamodel.MediaKindAudio {
			_, asset = m.screen.resolveCachedGUIArtwork(path, "audio/mpeg", true)
		} else {
			asset, _ = mediaartwork.Resolve(m.ctx, mediaartwork.Request{Path: path, Kind: kind, FFmpegPath: ffmpegPath})
		}
		v.ArtURL = m.artURL(asset)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.last.Path != path {
		return
	}
	m.last.Name, m.last.Artist, m.last.Album, m.last.ArtURL = v.Name, v.Artist, v.Album, v.ArtURL
	m.service.Update(m.last)
}

func (m *guiMPRIS) artURL(asset *metadata.ArtworkAsset) string {
	if asset == nil {
		return ""
	}
	path := filepath.Join(m.dir, asset.ID+asset.Extension)
	if err := os.WriteFile(path, asset.Data, 0600); err != nil {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func (m *guiMPRIS) seek(position int64) {
	s := m.screen
	if position < 0 {
		return
	}
	seconds := int(position / 1_000_000)
	if client := s.activeChromecastPlaybackClient(); client != nil {
		if s.mediaDuration > 0 {
			chromecastTranscodedSeek(s, seconds)
		} else if err := client.Seek(seconds); err != nil {
			return
		}
	} else if tv := s.tvdata; tv != nil && tv.ControlURL != "" {
		if tv.Transcode {
			s.ffmpegSeek = seconds
			s.dlnaSeekRestart = true
			stopActionSync(s)
			playAction(s)
		} else if err := tv.SeekSoapCall(utils.SecondsToClockTime(seconds)); err != nil {
			return
		}
	} else {
		return
	}
	m.service.SetPositionUS(position, true)
	fyne.Do(func() {
		if s.CurrentPos != nil {
			_ = s.CurrentPos.Set(utils.SecondsToClockTime(seconds))
		}
	})
}

func (m *guiMPRIS) setVolume(value float64) {
	s := m.screen
	value = min(max(value, 0), 1)
	if client := s.activeChromecastPlaybackClient(); client != nil {
		if err := client.SetVolume(float32(value)); err != nil {
			return
		}
	} else if tv := s.tvdata; tv != nil && tv.RenderingControlURL != "" {
		if err := tv.SetVolumeSoapCall(strconv.Itoa(int(value * 100))); err != nil {
			return
		}
	} else {
		return
	}
	m.volume(value)
}

func (m *guiMPRIS) volume(value float64) {
	m.mu.Lock()
	if !m.closed {
		m.last.Volume = min(max(value, 0), 1)
	}
	m.mu.Unlock()
	m.refresh()
}

func (m *guiMPRIS) readVolume(path string) {
	s := m.screen
	var value float64
	if client := s.activeChromecastPlaybackClient(); client != nil {
		status, err := client.GetStatus()
		if err != nil {
			return
		}
		value = float64(status.Volume)
	} else if tv := s.tvdata; tv != nil && tv.RenderingControlURL != "" {
		level, err := tv.GetVolumeSoapCall()
		if err != nil {
			return
		}
		value = float64(level) / 100
	} else {
		return
	}
	m.mu.Lock()
	if m.closed || m.last.Path != path {
		m.mu.Unlock()
		return
	}
	m.last.Volume = min(max(value, 0), 1)
	m.mu.Unlock()
	m.refresh()
}

var _ mprisBridge = (*guiMPRIS)(nil)
