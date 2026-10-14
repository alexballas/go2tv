package gui

import (
	"testing"

	"go2tv.app/go2tv/v2/httphandlers"
)

func TestChromecastSlowTranscodeKeepsStartupTracking(t *testing.T) {
	ticks := 0
	for range 60 {
		ticks = chromecastStartupIdleCount(ticks, httphandlers.TranscodeStatus{Active: true})
		if ticks >= chromecastStartupIdleTicks {
			t.Fatal("slow transcode discarded playback tracking before its first frame")
		}
	}
	// Once the HTTP transcode ends, a receiver that remains IDLE still times out.
	for range chromecastStartupIdleTicks {
		ticks = chromecastStartupIdleCount(ticks, httphandlers.TranscodeStatus{})
	}
	if ticks != chromecastStartupIdleTicks {
		t.Fatal("idle receiver never times out after transcoding ends")
	}
}
