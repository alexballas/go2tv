//go:build android || ios

package gui

import (
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
)

func newMobileSettingsSection(title string, content fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(
		widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		content,
	)
}

func mobileSettingsWindow(s *FyneScreen) (fyne.CanvasObject, func()) {
	app := fyne.CurrentApp()
	prefs := app.Preferences()

	themeSelect := widget.NewSelect(
		[]string{lang.L("System Default"), lang.L("Light"), lang.L("Dark")},
		func(selected string) {
			var themeName string
			switch selected {
			case lang.L("Light"):
				themeName = "Light"
			case lang.L("Dark"):
				themeName = "Dark"
			default:
				themeName = "System Default"
			}
			prefs.SetString("Theme", themeName)
			app.Settings().SetTheme(go2tvTheme{themeName})
		},
	)
	themeName := prefs.StringWithFallback("Theme", "System Default")
	themeSelect.SetSelected(lang.L(themeName))

	rememberPlayback := widget.NewCheck(lang.L("Remember Playback Position"), func(enabled bool) {
		prefs.SetBool(rememberPlaybackPositionPref, enabled)
	})
	rememberPlayback.SetChecked(prefs.BoolWithFallback(rememberPlaybackPositionPref, false))
	clearPlaybackHistory := widget.NewButtonWithIcon(lang.L("Clear Playback History"), theme.DeleteIcon(), func() {
		store := currentResumeStore()
		if store == nil {
			return
		}

		if err := store.clear(); err != nil {
			dialog.ShowError(err, s.Current)
			return
		}

		s.clearResumeSession()
		dialog.ShowInformation(lang.L("Playback History"), lang.L("Playback history cleared"), s.Current)
	})

	disableUpdates := widget.NewCheck(lang.L("Disable Future Version Notifications"), func(disabled bool) {
		prefs.SetBool(disableVersionNotificationsPref, disabled)
	})
	disableUpdates.SetChecked(prefs.BoolWithFallback(disableVersionNotificationsPref, false))

	generalSettings := container.NewVBox(
		widget.NewForm(widget.NewFormItem(lang.L("Theme"), themeSelect)),
		disableUpdates,
	)
	playbackSettings := container.NewVBox(
		rememberPlayback,
		container.NewHBox(clearPlaybackHistory),
	)
	settings := []fyne.CanvasObject{
		newMobileSettingsSection(lang.L("General"), generalSettings),
		widget.NewSeparator(),
		newMobileSettingsSection(lang.L("Playback"), playbackSettings),
	}

	batterySettings, refreshBatterySettings := newBatteryOptimizationSettings()
	if batterySettings != nil {
		settings = append(settings, batterySettings)
	}

	return container.NewVBox(settings...), refreshBatterySettings
}
