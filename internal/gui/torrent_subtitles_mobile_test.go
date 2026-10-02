//go:build android || ios

package gui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/mkvsubs"
)

type mobileCaptionSource struct {
	mediasource.Source
	path string
	size int64
}

func (s mobileCaptionSource) Open(context.Context) (io.ReadSeekCloser, error) { return os.Open(s.path) }
func (s mobileCaptionSource) Size() int64                                     { return s.size }

func TestMobileTorrentCaptionsSelectionAndSeek(t *testing.T) {
	s := newMobileTorrentTestScreen(t)
	s.httpserver = httphandlers.NewServer("")
	fixture := "../mkvsubs/testdata/torrent-text.mkv"
	info, err := os.Stat(fixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "movie.mkv")
	t.Cleanup(mediasource.Register(path, mobileCaptionSource{path: fixture, size: info.Size()}))
	s.mediafile = storage.NewFileURI(path)
	tt := []struct {
		name                       string
		disabled, external, remote bool
		offset                     int
		text                       string
	}{
		{name: "automatic without desktop checkbox", text: "First caption"},
		{name: "transcoded seek", offset: 30, text: "Second caption"},
		{name: "disabled", disabled: true},
		{name: "external also overrides burn-in", external: true},
		{name: "remote media", remote: true},
		{name: "automatic restored", text: "First caption"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s.TorrentSubsCheck = nil
			if tc.disabled {
				s.TorrentSubsCheck = widget.NewCheck("", nil)
			}
			s.subsfile = nil
			if tc.external {
				s.subsfile = storage.NewFileURI("/external.srt")
			}
			s.ExternalMediaURL.SetChecked(tc.remote)
			endpoint := registerMobileTorrentSubtitles(s, "host:1234", tc.offset)
			response := httptest.NewRecorder()
			s.httpserver.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://host:1234/torrent-subtitles.json?time=0", nil))
			if tc.text == "" {
				if endpoint != "" || response.Code != http.StatusNotFound {
					t.Fatalf("retained captions: %q %d", endpoint, response.Code)
				}
				return
			}
			var result struct{ Cues []mkvsubs.Cue }
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if endpoint == "" || response.Code != http.StatusOK || len(result.Cues) != 1 || result.Cues[0] != (mkvsubs.Cue{Start: 5, End: 10, Text: tc.text}) {
				t.Fatalf("captions: %q %d %+v", endpoint, response.Code, result.Cues)
			}
		})
	}
	s.mediafile = nil
	if endpoint := registerMobileTorrentSubtitles(s, "host:1234", 0); endpoint != "" {
		t.Fatalf("cleared media retained captions: %s", endpoint)
	}
}
