//go:build android || ios

package gui

import (
	"fmt"
	"path/filepath"

	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/utils"
)

// mobileSubtitleControls greys out subtitles for audio (radio included) and
// images, like the desktop selector.
type mobileSubtitleControls struct {
	browse, clear *widget.Button
	field         *valueField
	placeholder   string
}

// refresh follows the current media input and drops any subtitle already
// picked for media that can't use one.
func (c *mobileSubtitleControls) refresh(screen *FyneScreen, mediaInput string) {
	reason := ""
	switch screen.subtitleKindForInput(mediaInput) {
	case mediamodel.MediaKindAudio:
		reason = lang.L("Not available for audio")
	case mediamodel.MediaKindImage:
		reason = lang.L("Not available for images")
	}
	if reason == "" {
		c.placeholder = lang.L("No subtitle selected")
		c.field.OnTapped = func() { subsAction(screen) }
		c.browse.Enable()
		c.clear.Enable()
		c.field.Set(screen.SubsText.Text, c.placeholder)
		return
	}
	c.placeholder = reason
	if screen.subsfile != nil {
		clearsubsAction(screen)
	}
	c.field.OnTapped = nil
	c.browse.Disable()
	c.clear.Disable()
	c.field.Set("", reason)
}

func registerMobileChromecastSubtitles(screen *FyneScreen, host string, offset int, transcode bool) (string, error) {
	if screen.httpserver == nil {
		return "", nil
	}
	screen.httpserver.RemoveHandler("/subtitles.vtt")
	if host == "" || !hasChromecastMobileSubtitles(screen) || (transcode && screen.castBurnSubtitles) {
		return "", nil
	}
	if !transcode {
		offset = 0
	}
	source, err := storage.Reader(screen.subsfile)
	if err != nil {
		return "", fmt.Errorf("open subtitles: %w", err)
	}
	defer source.Close()
	data, err := utils.SubtitlesReaderForPlaybackContext(screen.playbackStartupContext(), source, filepath.Ext(screen.SubsText.Text), offset, screen.ffmpegPath)
	if err != nil {
		return "", fmt.Errorf("subtitle conversion: %w", err)
	}
	screen.httpserver.AddHandler("/subtitles.vtt", nil, nil, data)
	return "http://" + host + "/subtitles.vtt", nil
}
