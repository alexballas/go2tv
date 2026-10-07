//go:build !(android || ios)

package gui

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/lang"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/utils"
)

func extractChromecastSubtitles(screen *FyneScreen) error {
	ctx := screen.playbackStartupContext()
	screen.embeddedSubtitle = nil
	if screen.Screencast || torrentMediaSelected(screen) {
		return nil
	}
	track := -1
	automatic := false
	fyne.DoAndWait(func() {
		selected := screen.SelectInternalSubs.Selected
		switch {
		case selected != "":
			track = slices.Index(screen.SelectInternalSubs.Options, selected)
		case !screen.CustomSubsCheck.Checked && (screen.subsfile == "" || slices.Contains(screen.tempFiles, screen.subsfile)) &&
			!screen.ExternalMediaURL.Checked && screen.mediaKindForPath(screen.mediafile) == "video" &&
			len(screen.SelectInternalSubs.Options) > 0:
			// Automatic prefers a sidecar, then the first embedded track.
			track = 0
			automatic = true
		}
		if track >= 0 {
			screen.PlayPause.SetText(lang.L("Extracting Subtitles") + "   ")
		}
	})
	if track < 0 {
		return nil
	}
	if screen.Transcode && screen.castBurnSubtitles {
		original, err := utils.EmbeddedSubtitleForBurnContext(ctx, screen.ffmpegPath, screen.mediafile, track)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil && original != nil {
			screen.embeddedSubtitle = original
			screen.subsfile = ""
			fyne.Do(func() { screen.PlayPause.SetText(lang.L("Play") + "   ") })
			return nil
		}
		// Keep extraction's existing error/fallback behavior if probing fails.
	}
	path, err := utils.ExtractSubContext(ctx, screen.ffmpegPath, track, screen.mediafile)
	if ctx.Err() != nil {
		if path != "" {
			_ = os.Remove(path)
		}
		return ctx.Err()
	}
	fyne.Do(func() {
		screen.PlayPause.SetText(lang.L("Play") + "   ")
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if automatic {
			// Optional captions must not prevent video playback (e.g. bitmap
			// tracks cannot be converted to SRT). Explicit selections report errors.
			return nil
		}
		return err
	}
	screen.tempFiles = append(screen.tempFiles, path)
	screen.subsfile = path
	return nil
}

// registerChromecastSubtitles prepares simplified receiver captions for playback
// without burn-in. ASS/SSA typesetting is preserved only by the burn-in path.
func registerChromecastSubtitles(server *httphandlers.HTTPserver, host, path string, seekSeconds int, ffmpegPath ...string) (string, error) {
	return registerChromecastSubtitlesContext(context.Background(), server, host, path, seekSeconds, ffmpegPath...)
}

func registerChromecastSubtitlesContext(ctx context.Context, server *httphandlers.HTTPserver, host, path string, seekSeconds int, ffmpegPath ...string) (string, error) {
	if server == nil || host == "" {
		return "", nil
	}
	server.RemoveHandler("/subtitles.vtt")
	path, ok := playback.ChromecastSubtitlePath(path)
	if !ok {
		return "", nil
	}
	data, err := utils.SubtitlesForPlaybackContext(ctx, path, seekSeconds, ffmpegPath...)
	if err != nil {
		return "", fmt.Errorf("subtitle conversion: %w", err)
	}
	server.AddHandler("/subtitles.vtt", nil, nil, data)
	return "http://" + host + "/subtitles.vtt", nil
}

func (screen *FyneScreen) chromecastSubtitleBurnPath(transcoded bool) string {
	// RTMP serves existing HLS segments rather than this FFmpeg pipeline.
	if !transcoded || !screen.castBurnSubtitles || (screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked) {
		return ""
	}
	path, _ := playback.ChromecastSubtitlePath(screen.subsfile)
	return path
}

func desktopChromecastTranscodeOptions(screen *FyneScreen, seekSeconds int) *utils.TranscodeOptions {
	return &utils.TranscodeOptions{
		FFmpegPath:       screen.ffmpegPath,
		SubsPath:         screen.chromecastSubtitleBurnPath(true),
		SeekSeconds:      seekSeconds,
		SubtitleSize:     utils.SubtitleSizeMedium,
		LogOutput:        screen.Debug,
		TorrentSource:    screen.chromecastTorrentBurnSource(true),
		EmbeddedSubtitle: screen.embeddedSubtitle,
	}
}

func (screen *FyneScreen) chromecastTorrentBurnSource(transcoded bool) mediasource.Source {
	if !transcoded || !screen.castBurnSubtitles || screen.subsfile != "" || screen.Screencast ||
		(screen.CustomSubsCheck != nil && screen.CustomSubsCheck.Checked) ||
		(screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked) {
		return nil
	}
	return torrentSubtitleSource(screen.mediafile, true)
}

func registerDesktopChromecastSubtitles(screen *FyneScreen, server *httphandlers.HTTPserver, host string, seekSeconds int, transcoded bool) (string, error) {
	if screen.chromecastSubtitleBurnPath(transcoded) != "" || (transcoded && screen.castBurnSubtitles && screen.embeddedSubtitle != nil) {
		if server != nil {
			server.RemoveHandler("/subtitles.vtt")
		}
		return "", nil
	}
	if !transcoded {
		seekSeconds = 0
	}
	return registerChromecastSubtitlesContext(screen.playbackStartupContext(), server, host, screen.subsfile, seekSeconds, screen.ffmpegPath)
}
