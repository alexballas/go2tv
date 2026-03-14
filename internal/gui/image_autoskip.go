//go:build !(android || ios)

package gui

import (
	"context"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"go2tv.app/go2tv/v2/devices"
)

const (
	imageAutoSkipSecondsPref = "ImageAutoSkipSeconds"
	imageAutoSkipSecondsMin  = 5
	imageAutoSkipSecondsMax  = 300
)

func clampImageAutoSkipSeconds(seconds int) int {
	switch {
	case seconds < 0:
		return 0
	case seconds > 0 && seconds < imageAutoSkipSecondsMin:
		return imageAutoSkipSecondsMin
	case seconds > imageAutoSkipSecondsMax:
		return imageAutoSkipSecondsMax
	default:
		return seconds
	}
}

func getImageAutoSkipSecondsPref() int {
	return clampImageAutoSkipSeconds(
		fyne.CurrentApp().Preferences().IntWithFallback(imageAutoSkipSecondsPref, 10),
	)
}

func (p *FyneScreen) cancelImageAutoSkipTimer() {
	p.mu.Lock()
	cancel := p.imageAutoSkipCancel
	p.imageAutoSkipCancel = nil
	p.imageAutoSkipMediaPath = ""
	p.imageAutoSkipTimeout = 0
	p.imageAutoSkipID++
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (p *FyneScreen) configureImageAutoSkipTimer(mediaType, mediaPath string) {
	timeoutSeconds := getImageAutoSkipSecondsPref()
	state := p.getScreenState()

	if timeoutSeconds == 0 ||
		p.NextMediaCheck == nil ||
		!p.NextMediaCheck.Checked ||
		mediaPath == "" ||
		!strings.HasPrefix(mediaType, "image/") ||
		(p.ExternalMediaURL != nil && p.ExternalMediaURL.Checked) {
		p.cancelImageAutoSkipTimer()
		return
	}

	isChromecastImageSession := p.selectedDeviceType == devices.DeviceTypeChromecast &&
		p.chromecastClient != nil &&
		p.chromecastClient.IsConnected()

	if state != "Playing" && state != "Paused" && !isChromecastImageSession {
		p.cancelImageAutoSkipTimer()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

	p.mu.Lock()
	if p.imageAutoSkipCancel != nil &&
		p.imageAutoSkipMediaPath == mediaPath &&
		p.imageAutoSkipTimeout == timeoutSeconds {
		p.mu.Unlock()
		cancel()
		return
	}

	prevCancel := p.imageAutoSkipCancel
	p.imageAutoSkipID++
	timerID := p.imageAutoSkipID
	p.imageAutoSkipCancel = cancel
	p.imageAutoSkipMediaPath = mediaPath
	p.imageAutoSkipTimeout = timeoutSeconds
	p.mu.Unlock()

	if prevCancel != nil {
		prevCancel()
	}

	go func(id uint64, expectedMedia string, timeout int) {
		timer := time.NewTimer(time.Duration(timeout) * time.Second)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		p.mu.Lock()
		if p.imageAutoSkipID != id {
			p.mu.Unlock()
			return
		}
		p.imageAutoSkipCancel = nil
		p.imageAutoSkipMediaPath = ""
		p.imageAutoSkipTimeout = 0
		p.mu.Unlock()

		if p.NextMediaCheck == nil || !p.NextMediaCheck.Checked {
			return
		}
		if p.ExternalMediaURL != nil && p.ExternalMediaURL.Checked {
			return
		}

		p.mu.RLock()
		currentMedia := p.mediafile
		currentType := p.castingMediaType
		p.mu.RUnlock()

		if currentMedia != expectedMedia || !strings.HasPrefix(currentType, "image/") {
			return
		}

		fyne.Do(func() {
			skipNextAction(p)
		})
	}(timerID, mediaPath, timeoutSeconds)
}

func (p *FyneScreen) refreshImageAutoSkipTimer() {
	p.mu.RLock()
	mediaType := p.castingMediaType
	mediaPath := p.mediafile
	p.mu.RUnlock()

	p.configureImageAutoSkipTimer(mediaType, mediaPath)
}
