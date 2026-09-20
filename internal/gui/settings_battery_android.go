//go:build android

package gui

import (
	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/driver/mobile"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
)

func newBatteryOptimizationSettings() (fyne.CanvasObject, func()) {
	statusIcon := widget.NewIcon(theme.ConfirmIcon())
	status := widget.NewLabel("")
	description := widget.NewLabel("")
	description.Wrapping = fyne.TextWrapWord

	openSettings := widget.NewButton(lang.L("Open battery settings"), func() {
		power, ok := fyne.CurrentApp().Driver().(mobile.BatteryOptimization)
		if ok {
			power.RequestBatteryOptimizationExemption()
		}
	})

	section := container.NewVBox(
		widget.NewSeparator(),
		newMobileSettingsSection(
			lang.L("Background casting"),
			container.NewVBox(
				container.NewHBox(statusIcon, status),
				description,
				openSettings,
			),
		),
	)

	refresh := func() {
		power, ok := fyne.CurrentApp().Driver().(mobile.BatteryOptimization)
		if !ok {
			section.Hide()
			return
		}

		section.Show()
		if power.IsIgnoringBatteryOptimizations() {
			statusIcon.SetResource(theme.ConfirmIcon())
			status.SetText(lang.L("Enabled"))
			description.SetText(lang.L("Casting is less likely to be interrupted when your screen is off."))
			openSettings.Hide()
			return
		}

		statusIcon.SetResource(theme.WarningIcon())
		status.SetText(lang.L("Setup needed"))
		description.SetText(lang.L("Android may interrupt casting when your screen is off. Open battery settings and set Go2TV to Unrestricted."))
		openSettings.Show()
	}
	refresh()

	return section, refresh
}
