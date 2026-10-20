package main

import (
	"os"
	"testing"
)

func TestSubtitleFlagSelection(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, name := range []string{"movie.srt", "custom.srt"} {
		if err := os.WriteFile(name, []byte("captions"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	originalMedia, originalSubs := *mediaArg, *subsArg
	t.Cleanup(func() { *mediaArg, *subsArg = originalMedia, originalSubs })
	tt := []struct {
		name, media, subtitles, want string
		wantError                    bool
	}{
		{name: "missing local sidecar", media: "missing.mkv"},
		{name: "missing torrent sidecar", media: "missing.torrent"},
		{name: "magnet without sidecar"},
		{name: "stdin without sidecar", media: "-"},
		{name: "existing local sidecar", media: "movie.mkv", want: "movie.srt"},
		{name: "existing torrent sidecar", media: "movie.torrent", want: "movie.srt"},
		{name: "explicit subtitles override inference", media: "movie.torrent", subtitles: "custom.srt", want: "custom.srt"},
		{name: "explicit missing subtitles rejected", media: "movie.torrent", subtitles: "missing.srt", wantError: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			*mediaArg, *subsArg = tc.media, tc.subtitles
			err := checkSflag()
			if (err != nil) != tc.wantError {
				t.Fatalf("subtitle selection error = %v, wantError=%v", err, tc.wantError)
			}
			if tc.wantError {
				return
			}
			if *subsArg != tc.want {
				t.Fatalf("subtitles = %q, want %q", *subsArg, tc.want)
			}
		})
	}
}
