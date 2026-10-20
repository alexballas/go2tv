package gui

import (
	"fmt"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/dialog"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/soapcalls"
)

// Receivers can report IDLE until FFmpeg has prepared captions and buffered
// video. Keep polling while the HTTP transcode is alive; Stop still cancels it.
func chromecastStartupIdleCount(ticks int, status httphandlers.TranscodeStatus) int {
	if status.Active {
		return 0
	}
	return ticks + 1
}

func stopFailedTranscode(screen *FyneScreen, server *httphandlers.HTTPserver, payload *soapcalls.TVPayload, actionID uint64) bool {
	mediaURL := ""
	if payload != nil {
		mediaURL = payload.MediaURL
	}
	status := server.TranscodeStatus(mediaURL)
	if status.Err == nil {
		return false
	}
	fyne.Do(func() {
		// A late request must not stop replacement playback or a queued item.
		if screen.httpserver != server || (payload == nil && !screen.isChromecastActionCurrent(actionID)) ||
			(payload != nil && (screen.tvdata != payload || payload.MediaURL != mediaURL)) {
			return
		}
		if server.TranscodeStatus(mediaURL).Err == nil {
			return
		}
		stopAction(screen)
		dialog.ShowError(fmt.Errorf("video transcode: %w", status.Err), screen.Current)
	})
	return true
}
