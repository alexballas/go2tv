//go:build !(android || ios)

package gui

import (
	"testing"

	fyne "github.com/alexballas/refyne/v2"
)

func TestInitialWindowSize(t *testing.T) {
	tests := []struct {
		name string
		min  fyne.Size
		want fyne.Size
	}{
		{
			name: "default clears settings breakpoint",
			min:  fyne.NewSize(800, 600),
			want: fyne.NewSize(1024, 660),
		},
		{
			name: "content minimum wins",
			min:  fyne.NewSize(1200, 800),
			want: fyne.NewSize(1200, 800),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := initialWindowSize(tt.min); got != tt.want {
				t.Fatalf("initialWindowSize(%v) = %v, want %v", tt.min, got, tt.want)
			}
		})
	}
}
