//go:build !(android || ios)

package gui

import (
	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/dialog"
)

func showFilePicker(picker dialog.Dialog, parent fyne.Window) {
	picker.Show()
	picker.Resize(parent.Canvas().Size())
}
