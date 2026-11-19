package gui

import (
	"strconv"
	"sync"

	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	"go2tv.app/go2tv/v2/devices"
)

const relaxedDiscoveryPref = "RelaxedDiscovery"

var (
	relaxedOverrideMu sync.Mutex
	relaxedOverride   *bool
)

// SetRelaxedDiscoveryOverride records an explicit -relaxed-discovery flag: it
// wins over the stored preference for this session and is never persisted.
func SetRelaxedDiscoveryOverride(enabled bool) {
	relaxedOverrideMu.Lock()
	relaxedOverride = &enabled
	relaxedOverrideMu.Unlock()
}

func relaxedDiscoveryOverridden() bool {
	relaxedOverrideMu.Lock()
	defer relaxedOverrideMu.Unlock()
	return relaxedOverride != nil
}

// applyRelaxedDiscoveryPref loads the session's discovery mode into the
// process-wide setting: the command-line override if any, else the stored
// preference. Call it before building Settings and before
// devices.StartDiscovery.
func applyRelaxedDiscoveryPref() {
	relaxedOverrideMu.Lock()
	override := relaxedOverride
	relaxedOverrideMu.Unlock()
	switch {
	case override != nil:
		devices.SetRelaxedDiscovery(*override)
	case fyne.CurrentApp() != nil:
		devices.SetRelaxedDiscovery(fyne.CurrentApp().Preferences().BoolWithFallback(relaxedDiscoveryPref, false))
	}
}

// newRelaxedDiscoverySetting returns the relaxed-discovery checkbox with its
// warning, and a refresh func that re-syncs the checkbox and updates how many
// description URLs the last scan hid. The count is only shown where the user
// looks for it: a pop-up would let any LAN device nudge users into turning the
// protection off.
func newRelaxedDiscoverySetting(s *FyneScreen) (fyne.CanvasObject, func()) {
	hint := widget.NewLabel("")
	hint.Wrapping = fyne.TextWrapWord
	hint.SizeName = theme.SizeNameCaptionText

	var syncing bool
	check := newWrappingCheck(lang.L("Relaxed Device Discovery"), nil)
	refresh := func() {
		syncing = true
		check.SetChecked(devices.RelaxedDiscovery())
		syncing = false

		text := lang.L("Finds devices behind relays, with several addresses or on public-IP networks. Any device on your network can then make Go2TV contact other addresses. Leave off unless a device is missing.")
		if relaxedDiscoveryOverridden() {
			text += "\n" + lang.L("Set by -relaxed-discovery for this session")
		}
		if n := len(devices.HiddenSSDPLocations()); n > 0 && !devices.RelaxedDiscovery() {
			text += "\n" + lang.L("Hidden in last scan") + ": " + strconv.Itoa(n)
		}
		if hint.Text != text {
			hint.SetText(text)
		}
	}
	check.OnChanged = func(enabled bool) {
		if syncing {
			return
		}
		// A deliberate change ends any command-line override and is saved.
		relaxedOverrideMu.Lock()
		relaxedOverride = nil
		relaxedOverrideMu.Unlock()
		fyne.CurrentApp().Preferences().SetBool(relaxedDiscoveryPref, enabled)
		devices.SetRelaxedDiscovery(enabled)
		s.clearHiddenDeviceSelection()
		refresh()
	}
	refresh()

	return container.NewVBox(check, hint), refresh
}
