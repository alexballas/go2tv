package gui

import (
	"golang.org/x/sys/windows"
)

// NewFyneScreen .
func NewFyneScreen(version string) *FyneScreen {
	hideConsole()
	return initFyneNewScreen(version)
}

func hideConsole() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	showWindow := windows.NewLazySystemDLL("user32.dll").NewProc("ShowWindow")

	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd != 0 {
		const SW_HIDE = 0
		showWindow.Call(hwnd, SW_HIDE)
	}
}
