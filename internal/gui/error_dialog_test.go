//go:build !(android || ios)

package gui

import (
	"errors"
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"
)

func TestCheckInWindowUsesSpecifiedParent(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	mainWindow := app.NewWindow("main")
	remoteWindow := app.NewWindow("remote")
	screen := &FyneScreen{Current: mainWindow}

	checkInWindow(screen, errors.New("session error"), remoteWindow)
	fyne.DoAndWait(func() {})

	if got := len(mainWindow.Canvas().Overlays().List()); got != 0 {
		t.Fatalf("main window overlays = %d, want 0", got)
	}
	if got := len(remoteWindow.Canvas().Overlays().List()); got != 1 {
		t.Fatalf("remote window overlays = %d, want 1", got)
	}
}
