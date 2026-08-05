//go:build !(android || ios)

package gui

import (
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
)

func TestMuteButtonKeepsMuteIconAndHighlightsMutedState(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	screen := &FyneScreen{
		MuteUnmute: widget.NewButtonWithIcon("", theme.VolumeMuteIcon(), nil),
	}

	setMuteUnmuteView(true, screen)
	fyne.DoAndWait(func() {})
	if !screen.isMuted() {
		t.Fatal("expected muted state")
	}
	if screen.MuteUnmute.Icon != theme.VolumeMuteIcon() {
		t.Fatal("expected mute icon")
	}
	if screen.MuteUnmute.Importance != widget.DangerImportance {
		t.Fatalf("muted importance = %v, want danger", screen.MuteUnmute.Importance)
	}

	setMuteUnmuteView(false, screen)
	fyne.DoAndWait(func() {})
	if screen.isMuted() {
		t.Fatal("expected unmuted state")
	}
	if screen.MuteUnmute.Icon != theme.VolumeMuteIcon() {
		t.Fatal("expected mute icon to remain")
	}
	if screen.MuteUnmute.Importance != widget.LowImportance {
		t.Fatalf("unmuted importance = %v, want low", screen.MuteUnmute.Importance)
	}
}
