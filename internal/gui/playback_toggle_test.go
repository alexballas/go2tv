//go:build !(android || ios)

package gui

import (
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/test"
)

func TestPlaybackToggleInputAndModelStayInSync(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	toggle := newPlaybackToggle("Play in loop", playbackLoopIcon())
	calls := 0
	toggle.OnChanged = func(bool) { calls++ }
	w := app.NewWindow("Playback")
	defer w.Close()
	w.SetContent(container.New(playbackModesLayout{}, toggle))
	w.Resize(fyne.NewSize(400, 50))
	// Extra row width must stay outside the toggle hit area.
	renderer := test.WidgetRenderer(toggle).(*playbackToggleRenderer)
	if trailing := toggle.Size().Width - renderer.track.Position().X - renderer.track.Size().Width; trailing > 10 {
		t.Fatalf("toggle has %v pixels of trailing clickable space", trailing)
	}
	if toggle.Size().Width != toggle.MinSize().Width {
		t.Fatal("toggle hit area stretched into the empty row")
	}
	toggle.Tapped(&fyne.PointEvent{Position: fyne.NewPos(toggle.Size().Width-20, toggle.Size().Height/2)})
	if !toggle.Checked || calls != 1 {
		t.Fatal("clicking the right-hand switch must toggle the existing Check state once")
	}
	if w.Canvas().Focused() != toggle {
		t.Fatal("tapping must focus the toggle for keyboard use")
	}
	toggle.TypedRune(' ')
	if toggle.Checked || calls != 2 {
		t.Fatal("Space must toggle the same state")
	}
	toggle.Check.SetChecked(true)
	renderer = test.WidgetRenderer(toggle).(*playbackToggleRenderer)
	if renderer.thumb.Position().X <= renderer.track.Position().X+10 {
		t.Fatal("programmatic state changes must move the switch thumb")
	}
	toggle.Check.Disable()
	toggle.Tapped(&fyne.PointEvent{})
	toggle.TypedRune(' ')
	if !toggle.Checked || calls != 3 {
		t.Fatal("disabled toggles must ignore pointer and keyboard input")
	}
	toggle.Check.Enable()
	toggle.TypedRune(' ')
	if toggle.Checked || calls != 4 {
		t.Fatal("re-enabled toggle must respond again")
	}
}
