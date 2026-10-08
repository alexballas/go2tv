//go:build !(android || ios)

package gui

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/alexballas/refyne/v2/lang"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/mediasource"
)

func TestTorrentSubtitleSelectionAndHandlerLifetime(t *testing.T) {
	screen, card := newMediaCardTestScreen(t)
	path := filepath.Join(t.TempDir(), "torrent", "movie.mkv")
	t.Cleanup(mediasource.Register(path, &struct{ mediasource.Source }{}))
	screen.mediafile = path
	screen.ffmpegPath = filepath.Join(t.TempDir(), "missing-ffmpeg")
	screen.SelectInternalSubs.Options = []string{"Stale local track"}
	screen.SelectInternalSubs.SetSelected("Stale local track")
	if err := extractChromecastSubtitles(screen); err != nil || screen.subsfile != "" || len(screen.tempFiles) != 0 {
		t.Fatalf("torrent invoked local FFmpeg extraction: %v", err)
	}
	server := httphandlers.NewServer("")
	for _, mode := range []string{subtitleAutomatic, subtitleNone, subtitleExternal, subtitleEmbedded, subtitleAutomatic} {
		card.subtitles.SetSelected(lang.L(mode))
		url := registerTorrentSubtitles(server, "host:1234", path, !screen.CustomSubsCheck.Checked, 0)
		w := httptest.NewRecorder()
		server.ServeMediaHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://host:1234/torrent-subtitles.json?time=NaN", nil))
		if mode == subtitleAutomatic {
			if url != "http://host:1234/torrent-subtitles.json" || w.Code != http.StatusBadRequest {
				t.Fatalf("automatic torrent endpoint: %q %d", url, w.Code)
			}
		} else if url != "" || w.Code != http.StatusNotFound {
			t.Fatalf("explicit mode retained automatic endpoint: %s %q %d", mode, url, w.Code)
		}
	}
	if url := registerTorrentSubtitles(server, "host:1234", filepath.Join(t.TempDir(), "local.mkv"), true, 0); url != "" {
		t.Fatal("local file acquired torrent subtitle handler")
	}
	w := httptest.NewRecorder()
	server.ServeMediaHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://host:1234/torrent-subtitles.json?time=NaN", nil))
	if w.Code != http.StatusNotFound {
		t.Fatal("switching to local retained torrent endpoint")
	}
}
