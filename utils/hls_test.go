package utils

import "testing"

func TestIsHLSStream(t *testing.T) {
	tt := []struct {
		name      string
		mediaURL  string
		mediaType string
		want      bool
	}{
		{
			name:      "HLS URL extension",
			mediaURL:  "https://example.com/live/playlist.m3u8",
			mediaType: "",
			want:      true,
		},
		{
			name:      "HLS URL extension with query",
			mediaURL:  "https://example.com/live/playlist.m3u8?token=abc",
			mediaType: "",
			want:      true,
		},
		{
			name:      "HLS mime apple",
			mediaURL:  "https://example.com/live",
			mediaType: "application/vnd.apple.mpegurl",
			want:      true,
		},
		{
			name:      "HLS mime x-mpegurl",
			mediaURL:  "https://example.com/live",
			mediaType: "application/x-mpegURL",
			want:      true,
		},
		{
			name:      "non HLS",
			mediaURL:  "https://example.com/video.mp4",
			mediaType: "video/mp4",
			want:      false,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got := IsHLSStream(tc.mediaURL, tc.mediaType)
			if got != tc.want {
				t.Fatalf("%s: got: %t, want: %t", tc.name, got, tc.want)
			}
		})
	}
}
