//go:build !(android || ios)

package gui

import "github.com/alexballas/refyne/v2"

func platformThemeSize(fyne.ThemeSizeName) (float32, bool) { return 0, false }
