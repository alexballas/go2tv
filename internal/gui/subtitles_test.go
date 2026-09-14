//go:build !(android || ios)

package gui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go2tv.app/go2tv/v2/httphandlers"
)

func TestChromecastSubtitlesAcrossServerRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "embedded.srt")
	if err := os.WriteFile(path, []byte("1\n00:00:10,000 --> 00:00:12,000\nCaption\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tt := []struct {
		name string
		seek int
		want string
	}{
		{"transcoded playback", 0, "00:00:10.000 --> 00:00:12.000"},
		{"seek restarts server", 10, "00:00:00.000 --> 00:00:02.000"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			server := httphandlers.NewServer("127.0.0.1:0")
			url, err := registerChromecastSubtitles(server, "192.0.2.1:8080", path, tc.seek)
			if err != nil {
				t.Fatal(err)
			}
			if url != "http://192.0.2.1:8080/subtitles.vtt" {
				t.Fatalf("subtitle URL = %q", url)
			}
			recorder := httptest.NewRecorder()
			server.ServeMediaHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/subtitles.vtt", nil))
			if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), tc.want) || !strings.Contains(recorder.Body.String(), "Caption") {
				t.Fatalf("caption response: %d %s", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Type"); got != "text/vtt; charset=utf-8" {
				t.Fatalf("content type = %q", got)
			}
			if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
				t.Fatalf("CORS = %q", got)
			}
			// Moving to a file without subtitles must discard the previous track.
			url, err = registerChromecastSubtitles(server, "192.0.2.1:8080", "", 0)
			if err != nil || url != "" {
				t.Fatalf("cleared subtitles: URL = %q, error = %v", url, err)
			}
			recorder = httptest.NewRecorder()
			server.ServeMediaHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/subtitles.vtt", nil))
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("stale captions still served: %d", recorder.Code)
			}
		})
	}
}
