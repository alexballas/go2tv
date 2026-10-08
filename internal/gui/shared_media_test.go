package gui

import (
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/storage"
)

func TestSharedMediaRouting(t *testing.T) {
	tt := []struct {
		name, uri, mime   string
		accepted, torrent bool
	}{
		{"torrent MIME with opaque provider name", "content://documents/42", "application/x-bittorrent", true, true},
		{"alternate torrent MIME", "content://documents/42", "application/x-torrent", true, true},
		{"MIME parameters and casing", "content://documents/42", "Application/X-Bittorrent; charset=binary", true, true},
		{"torrent extension", "content://documents/movie.TORRENT", "application/octet-stream", true, true},
		{"torrent without MIME", "content://documents/movie.torrent", "", true, true},
		{"shared video", "content://documents/42", "video/mp4", true, false},
		{"shared audio", "content://documents/42", "audio/mpeg", true, false},
		{"shared image", "content://documents/42", "image/png", true, false},
		{"unspecified document", "content://documents/42", "", true, false},
		{"unsupported document", "content://documents/42", "application/pdf", false, false},
		{"remote URL", "https://example.org/movie.torrent", "application/x-bittorrent", false, true},
		{"missing URI", "", "application/x-bittorrent", false, false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			var uri fyne.URI
			if tc.uri != "" {
				var err error
				uri, err = storage.ParseURI(tc.uri)
				if err != nil {
					t.Fatalf("parse URI: %v", err)
				}
			}
			if got := acceptSharedURI(uri, tc.mime); got != tc.accepted {
				t.Fatalf("accepted = %v, want %v", got, tc.accepted)
			}
			if got := isTorrentDocument(uri, tc.mime); got != tc.torrent {
				t.Fatalf("torrent = %v, want %v", got, tc.torrent)
			}
		})
	}
}
