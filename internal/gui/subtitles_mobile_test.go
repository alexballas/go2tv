//go:build android || ios

package gui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexballas/refyne/v2/storage"

	"go2tv.app/go2tv/v2/httphandlers"
)

func TestMobileExternalCaptionsRenderingAndSeek(t *testing.T) {
	for _, ext := range []string{".srt", ".vtt"} {
		t.Run(ext, func(t *testing.T) {
			s := newMobileTorrentTestScreen(t)
			s.httpserver = httphandlers.NewServer("")
			data := "1\n00:00:05,000 --> 00:00:10,000\nFirst\n\n2\n00:00:35,000 --> 00:00:40,000\nSecond\n"
			if ext == ".vtt" {
				data = "WEBVTT\n\n00:00:05.000 --> 00:00:10.000\nFirst\n\n00:00:35.000 --> 00:00:40.000 align:start\nSecond\n"
			}
			path := filepath.Join(t.TempDir(), "captions"+ext)
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			s.subsfile = storage.NewFileURI(path)
			s.SubsText.SetText("captions" + ext)
			t.Cleanup(func() { removeTempFile(&s.tempSubsFile) })
			tt := []struct {
				name            string
				offset          int
				transcode, burn bool
			}{
				{"direct uses original timestamps", 30, false, false},
				{"transcoded seek", 30, true, false},
				{"seek back", 0, true, false},
				{"seek forward again", 30, true, false},
				{"burn fallback", 30, true, true},
				{"direct ignores fallback", 30, false, true},
				{"receiver restored", 30, true, false},
			}
			for _, tc := range tt {
				t.Run(tc.name, func(t *testing.T) {
					s.castBurnSubtitles = tc.burn
					endpoint, err := registerMobileChromecastSubtitles(s, "host:1234", tc.offset, tc.transcode)
					if err != nil {
						t.Fatal(err)
					}
					response := httptest.NewRecorder()
					s.httpserver.ServeMediaHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://host:1234/subtitles.vtt", nil))
					if tc.transcode && tc.burn {
						if endpoint != "" || response.Code != http.StatusNotFound {
							t.Fatalf("burn fallback kept receiver captions: %q %d", endpoint, response.Code)
						}
						opts, err := mobileTranscodeOptions(s)
						if err != nil {
							t.Fatal(err)
						}
						copied, err := os.ReadFile(opts.SubsPath)
						if err != nil || string(copied) != data {
							t.Fatalf("burn source: %q %v", copied, err)
						}
						return
					}
					text := response.Body.String()
					if endpoint == "" || response.Code != http.StatusOK || !strings.HasPrefix(text, "WEBVTT") {
						t.Fatalf("captions: %q %d %q", endpoint, response.Code, text)
					}
					if tc.transcode && tc.offset == 30 {
						if strings.Contains(text, "First") || !strings.Contains(text, "00:00:05.000 --> 00:00:10.000") {
							t.Fatalf("shifted captions: %q", text)
						}
					} else if !strings.Contains(text, "First") || !strings.Contains(text, "00:00:35.000 --> 00:00:40.000") {
						t.Fatalf("original captions: %q", text)
					}
					if !tc.burn {
						opts, err := mobileTranscodeOptions(s)
						if err != nil || opts.SubsPath != "" {
							t.Fatalf("receiver captions also burned: %+v %v", opts, err)
						}
					}
				})
			}
			s.castBurnSubtitles = false
			s.subsfile = storage.NewFileURI(filepath.Join(t.TempDir(), "missing.srt"))
			if _, err := registerMobileChromecastSubtitles(s, "host:1234", 0, false); err == nil {
				t.Fatal("subtitle open error hidden")
			}
		})
	}
}
