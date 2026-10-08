package gui

import (
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"
)

func TestSettingsCheckWrapsAndRemainsTappable(t *testing.T) {
	app := test.NewApp()
	t.Cleanup(app.Quit)
	w := app.NewWindow("Settings")
	t.Cleanup(w.Close)
	var enabled bool
	check := newWrappingCheck("Burn Chromecast Subtitles (Compatibility Fallback)", func(value bool) {
		enabled = value
	})
	next := widget.NewCheck("Remember Playback Position", nil)
	content := container.NewVBox(check, next)
	scroll := container.NewVScroll(content)
	w.SetContent(scroll)

	tt := []struct {
		name  string
		width float32
		wrap  bool
	}{
		{name: "phone", width: 320, wrap: true},
		{name: "large text or narrow phone", width: 240, wrap: true},
		{name: "landscape", width: 800},
		{name: "back to portrait", width: 320, wrap: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			w.Resize(fyne.NewSize(tc.width, 480))
			scroll.Refresh()
			r := test.WidgetRenderer(check).(*wrappingCheckRenderer)
			if tc.wrap && r.label.Size().Height <= widget.NewLabel("One line").MinSize().Height {
				t.Fatal("caption did not wrap")
			}
			if !tc.wrap && r.label.Size().Height != widget.NewLabel("One line").MinSize().Height {
				t.Fatal("caption did not return to one line")
			}
			if check.Size().Width > scroll.Size().Width || r.label.Position().X+r.label.Size().Width > check.Size().Width {
				t.Fatal("caption overflows viewport")
			}
			if r.label.Position().Y < 0 || r.label.Position().Y+r.label.Size().Height > check.Size().Height || next.Position().Y < check.Position().Y+check.Size().Height {
				t.Fatal("wrapped caption clipped or overlaps next setting")
			}
			check.SetChecked(false)
			test.TapAt(check, fyne.NewPos(check.Size().Width-10, r.label.Position().Y+r.label.Size().Height-10))
			if !check.Checked || !enabled {
				t.Fatal("tapping wrapped caption did not enable setting")
			}
			check.Disable()
			test.Tap(check)
			if !check.Checked || !enabled || r.label.Importance != widget.LowImportance {
				t.Fatal("disabled setting changed or caption lost disabled style")
			}
			check.Enable()
		})
	}
}
