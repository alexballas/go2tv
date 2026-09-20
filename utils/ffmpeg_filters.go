package utils

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// Bound output to 1080p without upscaling, with even dimensions for H.264.
const softwareTranscodeScaleFilter = "scale='min(1920,iw)':'min(1080,ih)':force_original_aspect_ratio=decrease,scale=trunc(iw/2)*2:trunc(ih/2)*2"

var ffmpegFilterCache sync.Map

// subtitleBurnFilter builds the ffmpeg "subtitles" filter used to burn
// subtitles into the video. It returns "" when no subtitles are configured or
// the ffmpeg build lacks the filter, and an error when the subtitle file's
// charset can't be detected.
func subtitleBurnFilter(ffmpegPath, subsPath string, size SubtitleSize) (string, error) {
	if subsPath == "" || !ffmpegFilterAvailable(ffmpegPath, "subtitles") {
		return "", nil
	}

	charenc, err := getCharDet(subsPath)
	if err != nil {
		return "", err
	}

	fontSize := 24
	switch size {
	case SubtitleSizeSmall:
		fontSize = 20
	case SubtitleSizeLarge:
		fontSize = 30
	}

	forceStyle := fmt.Sprintf(":force_style='FontSize=%d,Outline=1'", fontSize)
	escapedPath := escapeFFmpegPath(subsPath)

	if charenc == "UTF-8" {
		return fmt.Sprintf("subtitles='%s'%s", escapedPath, forceStyle), nil
	}

	return fmt.Sprintf("subtitles='%s':charenc=%s%s", escapedPath, charenc, forceStyle), nil
}

func ffmpegFilterAvailable(ffmpegPath, name string) bool {
	key := ffmpegPath + "|" + name
	if cached, ok := ffmpegFilterCache.Load(key); ok {
		return cached.(bool)
	}

	filters, err := ffmpegFilterSet(ffmpegPath)
	if err != nil {
		return true
	}

	_, ok := filters[name]
	ffmpegFilterCache.Store(key, ok)
	return ok
}

func ffmpegFilterSet(ffmpegPath string) (map[string]struct{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), transcodeEncoderProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffmpegPath, "-hide_banner", "-filters")
	setSysProcAttr(cmd)

	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("ffmpeg -filters timeout after %s", transcodeEncoderProbeTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("ffmpeg -filters failed: %w", err)
	}

	filters := make(map[string]struct{})
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		filters[fields[1]] = struct{}{}
	}

	return filters, nil
}
