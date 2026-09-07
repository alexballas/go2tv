//go:build !(android || ios)

package gui

import (
	"image/color"
	"testing"

	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
)

func TestSettingsControlsKeepWidthAndReflow(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	label := widget.NewLabel("Gapless Playback")
	control := widget.NewSelect([]string{"Enabled", "Disabled"}, nil)
	row := newSettingsRow(label, control)
	tt := []struct {
		name  string
		width float32
	}{
		{"desktop", 1000},
		{"wider window", 1400},
		{"narrow window", 500},
		{"desktop after narrowing", 1000},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			width := tc.width
			row.Resize(fyne.NewSize(width, 100))
			if width >= 1000 {
				if control.Size().Width != 430 {
					t.Fatalf("control width changed at row width %v: %v", width, control.Size().Width)
				}
				if control.Position().X+control.Size().Width != width {
					t.Fatalf("control is not right-aligned at row width %v", width)
				}
			} else if control.Position().Y < label.Size().Height {
				t.Fatalf("control overlaps label at width %v", width)
			}
			if control.Position().X+control.Size().Width > width {
				t.Fatalf("control overflows row at width %v", width)
			}
		})
	}
}

func TestMainTabStartupFitsContentWithSmallMargin(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	tt := []struct {
		name   string
		padded bool
	}{
		{"padded window", true},
		{"unpadded window", false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			padded := tc.padded
			w := app.NewWindow("startup")
			w.SetPadded(padded)
			left := canvas.NewRectangle(color.Transparent)
			left.SetMinSize(fyne.NewSize(740, 640))
			right := canvas.NewRectangle(color.Transparent)
			right.SetMinSize(fyne.NewSize(200, 240))
			columns := newResponsiveTwoColumnLayout(800, 0.66)
			main := container.NewScroll(container.NewPadded(container.New(
				columns, left, right,
			)))
			tabs := container.NewAppTabs(container.NewTabItem("Go2TV", main))
			w.SetContent(tabs)
			w.Resize(mainTabWindowSize(w, tabs, main))
			main.Refresh()
			if columns.stacked {
				t.Fatal("startup should fit playback beside Devices")
			}

			if main.Content.Size().Height > main.Size().Height {
				t.Fatalf("padded=%v: main tab overflows: content %v, viewport %v", padded, main.Content.Size(), main.Size())
			}
			gap := main.Size().Height - main.Content.MinSize().Height
			if gap < 0 || gap > 2*theme.Padding() {
				t.Fatalf("padded=%v: startup bottom gap = %v", padded, gap)
			}
			w.Close()
		})
	}
}
