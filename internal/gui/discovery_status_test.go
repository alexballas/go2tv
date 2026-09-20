package gui

import (
	"errors"
	"testing"
)

func TestDiscoveryStatusText(t *testing.T) {
	tests := []struct {
		name     string
		count    int
		searched bool
		err      error
		want     string
	}{
		{name: "searching", want: "Searching for devices…"},
		{name: "empty", searched: true, want: "No devices found"},
		{name: "one", count: 1, searched: true, want: "1 device found"},
		{name: "many", count: 3, searched: true, want: "3 devices found"},
		{name: "error", searched: true, err: errors.New("network unavailable"), want: "Device discovery error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := discoveryStatusText(tt.count, tt.searched, tt.err); got != tt.want {
				t.Fatalf("discoveryStatusText() = %q, want %q", got, tt.want)
			}
		})
	}
}
