package gui

import (
	"image/color"
	"testing"

	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
)

func TestResponsiveTwoColumnLayoutWidths(t *testing.T) {
	tests := []struct {
		name    string
		width   float32
		stacked bool
	}{
		{name: "desktop", width: 1000, stacked: false},
		{name: "narrow", width: 750, stacked: true},
		{name: "small", width: 560, stacked: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left := canvas.NewRectangle(color.Transparent)
			left.SetMinSize(fyne.NewSize(320, 300))
			right := canvas.NewRectangle(color.Transparent)
			right.SetMinSize(fyne.NewSize(220, 200))
			objects := []fyne.CanvasObject{left, right}
			responsive := newResponsiveTwoColumnLayout(800, 0.66)
			size := fyne.NewSize(tt.width, 700)

			responsive.Layout(objects, size)

			if responsive.stacked != tt.stacked {
				t.Fatalf("expected stacked=%t at width %.0f", tt.stacked, tt.width)
			}
			for i, object := range objects {
				if rightEdge := object.Position().X + object.Size().Width; rightEdge > size.Width {
					t.Fatalf("object %d exceeds width: %.0f > %.0f", i, rightEdge, size.Width)
				}
				if bottom := object.Position().Y + object.Size().Height; bottom > size.Height {
					t.Fatalf("object %d exceeds height: %.0f > %.0f", i, bottom, size.Height)
				}
			}
		})
	}
}

func TestResponsiveTwoColumnLayoutReflowsAfterWideLayout(t *testing.T) {
	left := canvas.NewRectangle(color.Transparent)
	left.SetMinSize(fyne.NewSize(320, 300))
	right := canvas.NewRectangle(color.Transparent)
	right.SetMinSize(fyne.NewSize(220, 200))
	objects := []fyne.CanvasObject{left, right}
	responsive := newResponsiveTwoColumnLayout(800, 0.66)

	responsive.Layout(objects, fyne.NewSize(1000, 700))
	responsive.Layout(objects, fyne.NewSize(560, 700))

	if !responsive.stacked {
		t.Fatal("expected layout to stack after narrowing")
	}
	if got := responsive.MinSize(objects).Width; got > 560 {
		t.Fatalf("minimum width prevents narrow reflow: %.0f", got)
	}
}

func TestResponsiveTwoColumnLayoutStacksForWideChild(t *testing.T) {
	left := canvas.NewRectangle(color.Transparent)
	left.SetMinSize(fyne.NewSize(600, 300))
	right := canvas.NewRectangle(color.Transparent)
	right.SetMinSize(fyne.NewSize(220, 200))
	objects := []fyne.CanvasObject{left, right}
	responsive := newResponsiveTwoColumnLayout(800, 0.66)

	responsive.Layout(objects, fyne.NewSize(800, 700))

	if !responsive.stacked {
		t.Fatal("expected wide child to trigger stacking above fixed breakpoint")
	}
}

func TestResponsiveTwoColumnLayoutUsesAvailableWidth(t *testing.T) {
	left := canvas.NewRectangle(color.Transparent)
	left.SetMinSize(fyne.NewSize(740, 300))
	right := canvas.NewRectangle(color.Transparent)
	right.SetMinSize(fyne.NewSize(220, 200))
	objects := []fyne.CanvasObject{left, right}
	responsive := newResponsiveTwoColumnLayout(800, 0.66)
	for _, width := range []float32{1120, 1000, 960} {
		responsive.Layout(objects, fyne.NewSize(width, 700))
		if responsive.stacked {
			t.Fatalf("columns fit but stacked at width %v", width)
		}
		if left.Size().Width < 740 || right.Size().Width < 220 {
			t.Fatal("columns must retain their minimum widths")
		}
	}
	responsive.Layout(objects, fyne.NewSize(950, 700))
	if !responsive.stacked {
		t.Fatal("columns must stack when their combined minimum exceeds the viewport")
	}
}
