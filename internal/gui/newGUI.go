//go:build !windows
// +build !windows

package gui

// NewFyneScreen .
func NewFyneScreen(version string) *FyneScreen {
	return initFyneNewScreen(version)
}
