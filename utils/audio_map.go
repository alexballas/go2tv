package utils

import "strconv"

// audioStreamMap selects the chosen audio stream, or the optional first one.
func audioStreamMap(track int) string {
	if track <= 0 {
		return "0:a:0?"
	}
	return "0:a:" + strconv.Itoa(track-1)
}

// selectedStreamMaps limits output to the main video (no cover art) and the
// chosen audio. Without a selection FFmpeg keeps its default stream choice.
func selectedStreamMaps(track int) []string {
	if track <= 0 {
		return nil
	}
	return []string{"-map", "0:V:0?", "-map", audioStreamMap(track)}
}
