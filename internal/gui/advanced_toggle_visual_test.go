//go:build !(android || ios)

package gui

import (
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/test"
	"image/png"
	"os"
	"testing"
)

func TestAdvancedToggleVisual(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	desktop := newPlaybackToggle("Cast Desktop (experimental)", playbackDesktopIcon())
	server := newPlaybackToggle("RTMP Server", playbackServerIcon())
	w := app.NewWindow("Advanced Options")
	defer w.Close()
	w.SetContent(newSectionCard("Advanced Options", container.NewVBox(container.New(playbackModesLayout{}, desktop, server))))
	for _, mode := range []string{"Light", "Dark"} {
		app.Settings().SetTheme(go2tvTheme{Theme: mode})
		w.Resize(fyne.NewSize(780, 100))
		f, err := os.Create("/tmp/go2tv-advanced-" + mode + ".png")
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, w.Canvas().Capture()); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
