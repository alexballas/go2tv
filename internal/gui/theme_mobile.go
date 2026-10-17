//go:build android || ios

package gui

import (
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/theme"
)

// Rounder controls match the platform look on rounded phone screens, and the
// larger inner padding gives Material's 40dp button height.
func platformThemeSize(name fyne.ThemeSizeName) (float32, bool) {
	switch name {
	case theme.SizeNameInnerPadding:
		return 10, true
	case theme.SizeNameInputRadius:
		return 12, true
	case theme.SizeNameSelectionRadius:
		return 8, true
	}
	return 0, false
}
