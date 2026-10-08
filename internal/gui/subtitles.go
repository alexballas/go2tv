//go:build !(android || ios)

package gui

import (
	"context"
	"errors"
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
	err := prepareDesktopSubtitles(screen, screen.mediafile, screen.Transcode && screen.castBurnSubtitles, true)
	if errors.Is(err, utils.ErrBitmapSubtitles) {
		return fmt.Errorf("%w. %s", utils.ErrBitmapSubtitles, lang.L("Enable Transcode and Burn Chromecast Subtitles in Settings → Playback, or select an external text subtitle file."))
	}
	return err
}

// prepareDesktopSubtitles shares local track selection across both protocols.
// DLNA leaves automatic embedded captions to the TV without transcoding.
func prepareDesktopSubtitles(screen *FyneScreen, mediaPath string, burn, automaticEmbedded bool) error {
	ctx := screen.playbackStartupContext()
	screen.embeddedSubtitle = nil
	if screen.Screencast || torrentMediaSelected(screen) ||
		(screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked) {
		return nil
	}
	var tracks []int
	automatic := false
	fyne.DoAndWait(func() {
		if !screen.CustomSubsCheck.Checked && !automaticEmbedded && slices.Contains(screen.tempFiles, screen.subsfile) {
			// A previous extraction must not override native DLNA captions.
			screen.subsfile = ""
		}
		selected := screen.SelectInternalSubs.Selected
		switch {
		case selected != "":
			if track := slices.Index(screen.SelectInternalSubs.Options, selected); track >= 0 {
				tracks = append(tracks, track)
			}
		case automaticEmbedded && !screen.CustomSubsCheck.Checked && (screen.subsfile == "" || slices.Contains(screen.tempFiles, screen.subsfile)) &&
			!screen.ExternalMediaURL.Checked && screen.mediaKindForPath(mediaPath) == "video":
			for track := range screen.SelectInternalSubs.Options {
				tracks = append(tracks, track)
			}
			automatic = true
		}
		if len(tracks) > 0 {
			screen.PlayPause.SetText(lang.L("Extracting Subtitles") + "   ")
		}
	})
	if len(tracks) == 0 {
		return nil
	}
	// Discard a previous extraction before retrying or switching to direct burn-in.
	screen.subsfile = ""
	path, original, err := prepareEmbeddedSubtitles(ctx, screen.ffmpegPath, mediaPath, tracks, burn, automatic)
	fyne.Do(func() { screen.PlayPause.SetText(lang.L("Play") + "   ") })
	if err != nil {
		if errors.Is(err, utils.ErrBitmapSubtitles) {
			return fmt.Errorf("%w. %s", err, lang.L("Enable Transcode to burn these subtitles into the video, or select an external text subtitle file."))
		}
		return err
	}
	if path != "" {
		screen.tempFiles = append(screen.tempFiles, path)
	}
	screen.subsfile = path
	screen.embeddedSubtitle = original
	return nil
}

func prepareEmbeddedSubtitles(ctx context.Context, ffmpegPath, mediaPath string, tracks []int, burn, automatic bool) (string, *utils.EmbeddedSubtitle, error) {
	for _, track := range tracks {
		if burn {
			original, err := utils.EmbeddedSubtitleForBurnContext(ctx, ffmpegPath, mediaPath, track)
			if ctx.Err() != nil {
				return "", nil, ctx.Err()
			}
			if err == nil && original != nil {
				return "", original, nil
			}
			// Fall back to extraction when the direct-render probe fails.
		}
		path, err := utils.ExtractSubContext(ctx, ffmpegPath, track, mediaPath)
		if ctx.Err() != nil {
			if path != "" {
				_ = os.Remove(path)
			}
			return "", nil, ctx.Err()
		}
		if err != nil {
			if automatic {
				// Bitmap or unreadable tracks must not block optional captions.
				continue
			}
			return "", nil, err
		}
		return path, nil, nil
	}
	return "", nil, ctx.Err()
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
