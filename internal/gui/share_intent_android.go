//go:build android

package gui

import (
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/driver/mobile"
)

// registerShareHandler makes go2tv respond to files handed to it by other apps,
// through the Android share sheet or "open with". The shared file lands in the
// media field exactly as if it had been chosen from Select Media File; picking a
// device and pressing Play stay manual.
//
// Call it after the window content exists - the callback touches MediaText - and
// note that it also runs before ShowAndRun, so the driver has no function queue
// yet. Delivery is buffered on the refyne side until both are ready.
func registerShareHandler(screen *FyneScreen) {
	opener, ok := fyne.CurrentApp().Driver().(mobile.URIOpener)
	if !ok {
		return
	}

	opener.SetOnOpenURI(func(uri fyne.URI, mime string) {
		handleSharedURI(screen, uri, mime)
	})
}

// handleSharedURI is called on the Fyne goroutine.
func handleSharedURI(screen *FyneScreen, uri fyne.URI, mime string) {
	if !acceptSharedURI(uri, mime) {
		return
	}
	if isTorrentDocument(uri, mime) {
		openMobileTorrentDocument(screen, uri)
		return
	}

	// Untick Media from URL before assigning, never after: SetChecked runs
	// OnChanged synchronously, and that restores the previously selected file,
	// which would overwrite the share. Doing it only once the share has been
	// accepted keeps a rejected one from clearing the user's URL mode.
	if screen.ExternalMediaURL != nil && screen.ExternalMediaURL.Checked {
		screen.ExternalMediaURL.SetChecked(false)
	}

	setMobileMediaURI(screen, uri)
}
