package utils

import (
	"testing"
	"time"
)

// TestFormatDuration - test formatDuration
func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected string
	}{
		{
			name:     "Zero duration",
			input:    0,
			expected: "00:00:00.000",
		},
		{
			name:     "Milliseconds only",
			input:    123 * time.Millisecond,
			expected: "00:00:00.123",
		},
		{
			name:     "Seconds and milliseconds",
			input:    12*time.Second + 345*time.Millisecond,
			expected: "00:00:12.345",
		},
		{
			name:     "Minutes, seconds, and milliseconds",
			input:    5*time.Minute + 23*time.Second + 789*time.Millisecond,
			expected: "00:05:23.789",
		},
		{
			name:     "Hours, minutes, seconds, and milliseconds",
			input:    2*time.Hour + 15*time.Minute + 9*time.Second + 56*time.Millisecond,
			expected: "02:15:09.056",
		},
		{
			name:     "More than a day",
			input:    26*time.Hour + 45*time.Minute + 33*time.Second + 1*time.Millisecond,
			expected: "26:45:33.001",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := formatDuration(test.input)
			if result != test.expected {
				t.Fatalf("for input %v, expected %q but got %q", test.input, test.expected, result)
			}
		})
	}
}
