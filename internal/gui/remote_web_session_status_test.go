//go:build !(android || ios)

package gui

import (
	"testing"

	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"
)

func TestRemoteSessionStatusViewTakesOverAndRestoresMainTab(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	desktopView := widget.NewLabel("desktop controls")
	screen := &FyneScreen{remoteSession: newRemoteSessionManager()}
	var status *remoteSessionStatusView
	fyne.DoAndWait(func() {
		status = newRemoteSessionStatusView(screen, desktopView)
		status.apply(remoteSessionSnapshot{
			State: remoteSessionRunning,
			URL:   "http://127.0.0.1:9666/",
		}, true)
	})

	if desktopView.Visible() {
		t.Fatal("desktop controls visible during remote session")
	}
	if !status.statusView.Visible() {
		t.Fatal("session status view hidden during remote session")
	}
	if got := status.headline.Text; got != "Remote Web Session active" {
		t.Fatalf("headline = %q", got)
	}
	wantExplanation := "Casting is controlled from the Web UI. This desktop session's playlist and playback are unavailable while the server is running."
	if got := status.explanation.Text; got != wantExplanation {
		t.Fatalf("explanation = %q", got)
	}
	if got := status.link.Text; got != "http://127.0.0.1:9666/" {
		t.Fatalf("link = %q", got)
	}
	if status.link.URL == nil || status.link.URL.String() != "http://127.0.0.1:9666/" {
		t.Fatalf("link target = %v", status.link.URL)
	}
	if !status.link.Visible() || !status.qrCode.Visible() || status.qrCode.Image == nil {
		t.Fatal("URL or QR code unavailable during remote session")
	}
	if got := status.qrCode.Image.Bounds().Size(); got.X != remoteSessionQRCodeSize || got.Y != remoteSessionQRCodeSize {
		t.Fatalf("QR code size = %v", got)
	}
	if status.openButton.Disabled() || status.stopButton.Disabled() {
		t.Fatal("session actions disabled while running")
	}

	fyne.DoAndWait(func() {
		status.apply(remoteSessionSnapshot{State: remoteSessionStopped}, false)
	})
	if !desktopView.Visible() {
		t.Fatal("desktop controls not restored after remote session")
	}
	if status.statusView.Visible() {
		t.Fatal("session status view still visible after remote session")
	}
}

func TestRemoteSessionStatusViewDisablesStopWhileStopping(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	screen := &FyneScreen{remoteSession: newRemoteSessionManager()}
	var status *remoteSessionStatusView
	fyne.DoAndWait(func() {
		status = newRemoteSessionStatusView(screen, widget.NewLabel("desktop controls"))
		status.apply(remoteSessionSnapshot{
			State: remoteSessionStopping,
			URL:   "http://127.0.0.1:9666/",
		}, true)
	})

	if !status.openButton.Disabled() {
		t.Fatal("Open Web UI enabled while stopping")
	}
	if !status.stopButton.Disabled() {
		t.Fatal("Stop Session enabled while stopping")
	}
}
