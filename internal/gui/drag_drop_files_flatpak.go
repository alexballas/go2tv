//go:build flatpak && !(android || ios)

package gui

import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/lang"
)

func onDropFiles(screen *FyneScreen) func(p fyne.Position, u []fyne.URI) {
	return func(p fyne.Position, u []fyne.URI) {
		check(screen, errors.New(lang.L("Drag and Drop is not supported in Flatpak builds.\nPlease use the 'Select Media File' button instead.")))

	}
}
