//go:build !(android || ios)

package gui

import (
	"testing"

	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"
)

func TestRemoteFailureLabelExplainsPortConflict(t *testing.T) {
	t.Parallel()
	if got := remoteFailureLabel(remoteFailureAddressInUse); got != "the selected port is already in use" {
		t.Fatalf("label = %q", got)
	}
}

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

func TestRemoteWebSessionDialogCloseClearsFailure(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	mainWindow := app.NewWindow("main")
	manager := newRemoteSessionManager()
	manager.mu.Lock()
	manager.state = remoteSessionFailed
	manager.lastError = remoteFailureAddressInUse
	manager.mu.Unlock()
	screen := &FyneScreen{Current: mainWindow, remoteSession: manager}

	fyne.DoAndWait(screen.openRemoteWebSessionDialog)
	closeButton := findRemoteDialogButton(mainWindow.Canvas().Overlays().Top(), "Close")
	if closeButton == nil {
		t.Fatal("Close button not found")
	}
	test.Tap(closeButton)
	fyne.DoAndWait(func() {})

	snapshot := manager.Snapshot()
	if snapshot.State != remoteSessionStopped {
		t.Fatalf("state after Close = %q, want %q", snapshot.State, remoteSessionStopped)
	}
	if snapshot.LastError != "" {
		t.Fatalf("last error after Close = %q", snapshot.LastError)
	}
}

func TestRemoteWebSessionDialogStateLayoutDoesNotShift(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	mainWindow := app.NewWindow("main")
	measure := func(state remoteSessionState, url, lastError, buttonText string) (fyne.Size, fyne.Position) {
		manager := newRemoteSessionManager()
		manager.mu.Lock()
		manager.state = state
		manager.url = url
		manager.lastError = lastError
		manager.mu.Unlock()
		screen := &FyneScreen{Current: mainWindow, remoteSession: manager}

		var size fyne.Size
		var position fyne.Position
		fyne.DoAndWait(func() {
			screen.openRemoteWebSessionDialog()
			size = screen.remoteDialog.MinSize()
			button := findRemoteDialogButton(mainWindow.Canvas().Overlays().Top(), buttonText)
			if button == nil {
				t.Fatalf("%s button not found; buttons: %v", buttonText, remoteDialogButtonTexts(mainWindow.Canvas().Overlays().Top()))
			}
			position = button.Position()
			screen.remoteDialog.Dismiss()
		})
		return size, position
	}

	runningSize, runningButton := measure(remoteSessionRunning, "http://127.0.0.1:9666/", "", "Stop Session")
	failedSize, failedButton := measure(remoteSessionFailed, "", remoteFailureAddressInUse, "Start Session")
	if failedSize.Height != runningSize.Height {
		t.Fatalf("failed dialog height = %v, running = %v", failedSize.Height, runningSize.Height)
	}
	if failedButton.Y != runningButton.Y {
		t.Fatalf("failed action Y = %v, running = %v", failedButton.Y, runningButton.Y)
	}
}

func remoteDialogButtonTexts(object fyne.CanvasObject) []string {
	var texts []string
	switch typed := object.(type) {
	case *widget.Button:
		return []string{typed.Text}
	case *widget.PopUp:
		return remoteDialogButtonTexts(typed.Content)
	case *widget.Card:
		return remoteDialogButtonTexts(typed.Content)
	case *fyne.Container:
		for _, child := range typed.Objects {
			texts = append(texts, remoteDialogButtonTexts(child)...)
		}
	}
	return texts
}

func findRemoteDialogButton(object fyne.CanvasObject, text string) *widget.Button {
	switch typed := object.(type) {
	case *widget.Button:
		if typed.Text == text {
			return typed
		}
	case *widget.PopUp:
		return findRemoteDialogButton(typed.Content, text)
	case *widget.Card:
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
