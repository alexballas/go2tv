//go:build !(android || ios)

package gui

import (
	"testing"

	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"
)

func TestRemoteWebSessionDialogCloseKeepsSessionRunning(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	mainWindow := app.NewWindow("main")
	manager := newRemoteSessionManager()
	manager.mu.Lock()
	manager.state = remoteSessionRunning
	manager.mu.Unlock()
	screen := &FyneScreen{Current: mainWindow, remoteSession: manager}

	fyne.DoAndWait(screen.openRemoteWebSessionDialog)
	if screen.remoteDialog == nil {
		t.Fatal("remote dialog is nil")
	}
	if got := len(mainWindow.Canvas().Overlays().List()); got != 1 {
		t.Fatalf("main window overlays = %d, want 1", got)
	}

	closeButton := findRemoteDialogButton(mainWindow.Canvas().Overlays().Top(), "Close")
	if closeButton == nil {
		t.Fatal("Close button not found")
	}
	test.Tap(closeButton)
	fyne.DoAndWait(func() {})

	if screen.remoteDialog != nil {
		t.Fatal("remote dialog retained after Close")
	}
	if got := len(mainWindow.Canvas().Overlays().List()); got != 0 {
		t.Fatalf("main window overlays after Close = %d, want 0", got)
	}
	if got := manager.Snapshot().State; got != remoteSessionRunning {
		t.Fatalf("session state after Close = %q, want %q", got, remoteSessionRunning)
	}
}

func findRemoteDialogButton(object fyne.CanvasObject, text string) *widget.Button {
	switch typed := object.(type) {
	case *widget.Button:
		if typed.Text == text {
			return typed
		}
	case *widget.PopUp:
		return findRemoteDialogButton(typed.Content, text)
	case *fyne.Container:
		for _, child := range typed.Objects {
			if button := findRemoteDialogButton(child, text); button != nil {
				return button
			}
		}
	}
	return nil
}
