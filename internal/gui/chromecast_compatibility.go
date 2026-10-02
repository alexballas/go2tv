//go:build !(android || ios)

package gui

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alexballas/refyne/v2"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/utils"
)

type chromecastCompatibilityProbe struct {
	path, ffmpeg string
	cancel       context.CancelFunc
}

// Called on the UI thread. Only Cast waits; device selection stays available.
func (s *FyneScreen) chromecastCompatibilityPending() bool {
	probe := s.chromecastProbe
	return probe != nil && s.selectedDeviceType == devices.DeviceTypeChromecast &&
		probe.path == s.mediafile && probe.ffmpeg == s.ffmpegPath
}

func (s *FyneScreen) checkChromecastCompatibility() {
	fyne.Do(func() {
		path, ffmpeg := s.mediafile, s.ffmpegPath
		if s.chromecastCompatibilityPending() {
			return
		}
		if s.chromecastProbe != nil {
			s.chromecastProbe.cancel()
			s.chromecastProbe = nil
			s.chromecastProbePending.Store(false)
			setPlayPauseView("", s)
		}
		// Remember successful checks so a user's manual override stays intact.
		if s.selectedDeviceType != devices.DeviceTypeChromecast || path == "" ||
			s.chromecastCheckedFile == path ||
			!slices.Contains(s.videoFormats, strings.ToLower(filepath.Ext(path))) {
			return
		}
		parent := s.torrent.ctx
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithCancel(parent)
		probe := &chromecastCompatibilityProbe{path: path, ffmpeg: ffmpeg, cancel: cancel}
		s.chromecastProbe = probe
		s.chromecastProbePending.Store(true)
		transcode := s.Transcode
		setPlayPauseView("", s)
		go func() {
			// FFmpeg validation and reading missing torrent pieces both stay
			// outside the event thread, including when there are no peers.
			info, err := utils.GetMediaCodecInfoContext(ctx, ffmpeg, path)
			fyne.Do(func() {
				defer cancel()
				if s.chromecastProbe != probe {
					return
				}
				s.chromecastProbe = nil
				s.chromecastProbePending.Store(false)
				defer setPlayPauseView("", s)
				if err != nil || ctx.Err() != nil || s.mediafile != path ||
					s.ffmpegPath != ffmpeg || s.selectedDeviceType != devices.DeviceTypeChromecast {
					return
				}
				s.chromecastCheckedFile = path
				if !utils.IsChromecastCompatible(info) && s.Transcode == transcode {
					s.TranscodeCheckBox.SetChecked(true)
					s.Transcode = true
				}
			})
		}()
	})
}
