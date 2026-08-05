//go:build android

package gui

import (
	_ "embed"
	"sync"

	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/driver/mobile"
	"github.com/alexballas/refyne/v2/lang"
	"go2tv.app/go2tv/v2/devices"
)

// The status bar draws a notification icon as a stencil: it keeps the alpha and
// discards the colours. The launcher icon is opaque corner to corner, so it comes
// out as a white block - hence a separate silhouette of the logo, white on
// transparent, rather than reusing go2TVIcon512.
//
//go:embed notification-icon.png
var notificationIconPNG []byte

var notificationIcon = fyne.NewStaticResource("go2tv-notification.png", notificationIconPNG)

// Android freezes a process a few seconds after it stops being visible, which
// stops every goroutine in it. For a cast that means the control channel goes
// quiet: a Chromecast receiver drops the connection when its PING goes
// unanswered, and a DLNA renderer expires the event subscription that
// RefreshLoopUUIDSoapCall was renewing. Both surface as a dead socket on the
// next write, once the user unlocks the phone and tries to press pause.
//
// A foreground service is what keeps the process out of the cached bucket, so
// one runs for as long as a cast session does. This is not tied to serving media
// locally: the control channel exists for every cast, including one where the
// device is fetching an external URL by itself, and that is the part that dies.
const batteryPromptShownPref = "AndroidBatteryPromptShown"

var backgroundSessionState struct {
	sync.Mutex
	running bool
}

// backgroundSessionDriver returns the driver hook, if this build is running on a
// platform that has one. The manifest entries it needs are declared through
// FyneApp.toml, so a mismatch shows up here rather than as a crash.
func backgroundSessionDriver() (mobile.BackgroundSession, bool) {
	d, ok := fyne.CurrentApp().Driver().(mobile.BackgroundSession)
	return d, ok
}

// prepareBackgroundSession is called once at startup. It asks for the
// notification permission ahead of the first cast, so the dialog does not land
// on top of the media the user just started, and lets discovery through the
// Wi-Fi multicast filter.
func prepareBackgroundSession(screen *FyneScreen) {
	if d, ok := backgroundSessionDriver(); ok {
		d.RequestNotificationPermission()
	}

	if locker, ok := fyne.CurrentApp().Driver().(mobile.MulticastLocker); ok {
		devices.SetMulticastGuard(locker.AcquireMulticastLock, locker.ReleaseMulticastLock)
	}
}

// beginBackgroundSession starts the foreground service while the app is visible.
// Android refuses a first start from the background, so the Play callback invokes
// this before moving casting work to a goroutine. Repeating it from another user
// action is safe: refyne updates the existing service.
func beginBackgroundSession(screen *FyneScreen) {
	d, ok := backgroundSessionDriver()
	if !ok {
		return
	}

	backgroundSessionState.Lock()
	defer backgroundSessionState.Unlock()

	d.StartBackgroundSession(lang.L("Casting"), backgroundSessionTarget(screen), notificationIcon)
	backgroundSessionState.running = true
}

// syncBackgroundSession stops the foreground service when playback ends and
// backs up the eager Play-callback start for sessions initiated by another path.
// Every terminal path funnels through updateScreenState, so Stop, EOF, and a lost
// device all release the service.
//
// A paused cast keeps the session: the renderer is still ours to control, and
// the connection that carries the resume still has to be maintained.
func syncBackgroundSession(screen *FyneScreen, state string) {
	want := state == "Playing" || state == "Paused"
	if want {
		backgroundSessionState.Lock()
		running := backgroundSessionState.running
		backgroundSessionState.Unlock()
		if !running {
			beginBackgroundSession(screen)
		}
		go maybePromptBatteryOptimization(screen)
		return
	}

	d, ok := backgroundSessionDriver()
	if !ok {
		return
	}

	backgroundSessionState.Lock()
	defer backgroundSessionState.Unlock()

	if !backgroundSessionState.running {
		return
	}

	d.StopBackgroundSession()
	backgroundSessionState.running = false
}

// backgroundSessionTarget names the device in the notification, so the entry
// says which player it belongs to rather than just that the app is busy.
func backgroundSessionTarget(screen *FyneScreen) string {
	if screen == nil || screen.selectedDevice.name == "" {
		return lang.L("Casting in progress")
	}
	return screen.selectedDevice.name
}

// maybePromptBatteryOptimization offers the exemption once, ever. The foreground
// service is what actually keeps the cast alive under the platform rules; this
// only covers vendors whose own task killers go further. It is not worth
// interrupting anyone twice for, so a dismissal is remembered.
func maybePromptBatteryOptimization(screen *FyneScreen) {
	power, ok := fyne.CurrentApp().Driver().(mobile.BatteryOptimization)
	if !ok || screen == nil || screen.Current == nil {
		return
	}

	prefs := fyne.CurrentApp().Preferences()
	if prefs.Bool(batteryPromptShownPref) || power.IsIgnoringBatteryOptimizations() {
		return
	}
	prefs.SetBool(batteryPromptShownPref, true)

	fyne.Do(func() {
		dialog.ShowConfirm(
			lang.L("Keep casting in the background"),
			lang.L("Some phones stop background apps to save battery, which interrupts casting. Exempt Go2TV from battery optimisation?"),
			func(exempt bool) {
				if exempt {
					power.RequestBatteryOptimizationExemption()
				}
			},
			screen.Current,
		)
	})
}
