package utils

import (
	"testing"
	"time"
)

// TestIsChromecastCompatible tests codec compatibility checking
func TestIsChromecastCompatible(t *testing.T) {
	tests := []struct {
		name     string
		info     *MediaCodecInfo
		expected bool
	}{
		{
			name: "Compatible MP4 H.264 AAC",
			info: &MediaCodecInfo{
				VideoCodec:   "h264",
				VideoProfile: "high",
				AudioCodec:   "aac",
				Container:    "mp4",
			},
			expected: true,
		},
		{
			name: "Compatible WebM VP9 Opus",
			info: &MediaCodecInfo{
				VideoCodec:   "vp9",
				VideoProfile: "",
				AudioCodec:   "opus",
				Container:    "webm",
			},
			expected: true,
		},
		{
			name: "Incompatible video codec (AVI)",
			info: &MediaCodecInfo{
				VideoCodec:   "mpeg4",
				VideoProfile: "",
				AudioCodec:   "aac",
				Container:    "avi",
			},
			expected: false,
		},
		{
			name: "Incompatible audio codec (DTS)",
			info: &MediaCodecInfo{
				VideoCodec:   "h264",
				VideoProfile: "high",
				AudioCodec:   "dts",
				Container:    "mp4",
			},
			expected: false,
		},
		{
			name: "Audio only (FLAC)",
			info: &MediaCodecInfo{
				VideoCodec:   "",
				VideoProfile: "",
				AudioCodec:   "flac",
				Container:    "mp4",
			},
			expected: true,
		},
		{
			name:     "Nil info",
			info:     nil,
			expected: false,
		},
		{
			name: "Compatible - ffprobe comma-separated format (matroska,webm)",
			info: &MediaCodecInfo{
				VideoCodec:   "h264",
				VideoProfile: "high",
				AudioCodec:   "aac",
				Container:    "matroska,webm",
			},
			expected: true,
		},
		{
			name: "Compatible - ffprobe comma-separated format (mov,mp4,m4a,3gp,3g2,mj2)",
			info: &MediaCodecInfo{
				VideoCodec:   "h264",
				VideoProfile: "high",
				AudioCodec:   "aac",
				Container:    "mov,mp4,m4a,3gp,3g2,mj2",
			},
			expected: true,
		},
		{
			name: "Incompatible - ffprobe comma-separated with no matching format",
			info: &MediaCodecInfo{
				VideoCodec:   "h264",
				VideoProfile: "high",
				AudioCodec:   "aac",
				Container:    "avi,divx",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsChromecastCompatible(tt.info)
			if result != tt.expected {
				t.Errorf("IsChromecastCompatible() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

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
