package gui

import (
	"strconv"

	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	"go2tv.app/go2tv/v2/devices"
)

const relaxedDiscoveryPref = "RelaxedDiscovery"

// applyRelaxedDiscoveryPref loads the stored discovery mode into the
// process-wide setting; call it before devices.StartDiscovery.
func applyRelaxedDiscoveryPref() {
	if app := fyne.CurrentApp(); app != nil {
		devices.SetRelaxedDiscovery(app.Preferences().BoolWithFallback(relaxedDiscoveryPref, false))
	}
}

// newRelaxedDiscoverySetting returns the relaxed-discovery checkbox with its
// warning, and a refresh func that updates how many replies the last scan hid.
// The count is only shown where the user looks for it: a pop-up would let any
// LAN device nudge users into turning the protection off.
func newRelaxedDiscoverySetting() (fyne.CanvasObject, func()) {
	hint := widget.NewLabel("")
	hint.Wrapping = fyne.TextWrapWord
	hint.SizeName = theme.SizeNameCaptionText
	refresh := func() {
		text := lang.L("Finds devices behind relays or with several addresses. Any device on your network can then make Go2TV contact other addresses. Leave off unless a device is missing.")
		if n := devices.HiddenSSDPLocations(); n > 0 && !devices.RelaxedDiscovery() {
			text += "\n" + lang.L("Hidden in last scan") + ": " + strconv.Itoa(n)
		}
		hint.SetText(text)
	}

	check := newWrappingCheck(lang.L("Relaxed Device Discovery"), nil)
	check.SetChecked(devices.RelaxedDiscovery())
	check.OnChanged = func(enabled bool) {
		fyne.CurrentApp().Preferences().SetBool(relaxedDiscoveryPref, enabled)
		devices.SetRelaxedDiscovery(enabled)
		refresh()
	}
	refresh()

	return container.NewVBox(check, hint), refresh
}
