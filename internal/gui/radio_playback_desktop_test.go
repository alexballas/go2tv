//go:build !(android || ios)

package gui

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/internal/radiobrowser"
)

// Exercise selection, SOAP metadata/URL construction and the actual receiver
// HTTP response together; policy helpers alone cannot protect this contract.
func TestRadioDLNAPlaybackFromSelection(t *testing.T) {
	ffmpeg, ctx, mp3, segment := radioPlaybackTestAudio(t)
	tt := []struct {
		name      string
		hls       bool
		transcode bool
	}{
		{name: "direct MP3"},
		{name: "transcoded MP3", transcode: true},
		{name: "direct HLS", hls: true},
		{name: "transcoded HLS", hls: true, transcode: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			upstream := radioPlaybackTestUpstream(t, mp3, segment)
			defer upstream.Close()
			station := radiobrowser.Station{UUID: "station", Name: "Radio & <Music>", URL: upstream.URL + "/radio.php?token=one", Codec: "MP3"}
			if tc.hls {
				station.URL, station.Codec, station.HLS = upstream.URL+"/playlist.php?token=one", "AAC", 1
			}
			loaded := make(chan radioDLNALoad, 1)
			played := make(chan struct{}, 1)
			renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "SUBSCRIBE" {
					w.Header().Set("SID", "uuid:radio-playback")
					w.Header().Set("TIMEOUT", "Second-300")
				}
				switch action := r.Header.Get("SOAPAction"); {
				case strings.Contains(action, "#SetAVTransportURI\""):
					var envelope struct {
						Load radioDLNALoad `xml:"Body>SetAVTransportURI"`
					}
					if err := xml.NewDecoder(r.Body).Decode(&envelope); err != nil {
						t.Errorf("decode renderer load: %v", err)
						http.Error(w, "invalid load", http.StatusBadRequest)
						return
					}
					loaded <- envelope.Load
				case strings.Contains(action, "#Play\""):
					played <- struct{}{}
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer renderer.Close()
			screen, card := newMediaCardTestScreen(t)
			screen.Current = fyne.CurrentApp().NewWindow("Radio playback")
			defer screen.Current.Close()
			screen.Current.SetContent(card.content)
			screen.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
			screen.CurrentPos, screen.EndPos = binding.NewString(), binding.NewString()
			screen.ffmpegPath = ffmpeg
			screen.TranscodeCheckBox = widget.NewCheck("Transcode", func(value bool) { screen.Transcode = value })
			screen.TranscodeCheckBox.SetChecked(tc.transcode)
			screen.controlURL, screen.eventURL = renderer.URL, renderer.URL
			screen.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
			screen.selectedDeviceType = devices.DeviceTypeDLNA
			radioWindow(ctx, screen)
			r := screen.radio
			r.client = radioTestDirectory{}
			fyne.DoAndWait(func() {
				r.setStations([]radiobrowser.Station{station})
				r.list.Select(0)
				test.Tap(r.useStation)
			})
			waitRadioUI(t, func() bool { return screen.MediaText.Text == station.StreamURL() })
			defer stopActionSync(screen)
			playAction(screen)
			var load radioDLNALoad
			select {
			case load = <-loaded:
			case <-ctx.Done():
				t.Fatal("radio selection did not reach renderer: ", ctx.Err())
			}
			select {
			case <-played:
			case <-ctx.Done():
				t.Fatal("renderer did not receive Play")
			}
			var metadata struct {
				Title string `xml:"item>title"`
				Class string `xml:"item>class"`
			}
			if err := xml.Unmarshal([]byte(load.Metadata), &metadata); err != nil || metadata.Title != station.Name {
				t.Fatalf("renderer station metadata: title=%q, error=%v", metadata.Title, err)
			}
			if metadata.Class != "object.item.audioItem.musicTrack" {
				t.Fatalf("renderer classified radio as %q", metadata.Class)
			}
			if tc.hls && !tc.transcode && load.URI != station.StreamURL() {
				t.Fatalf("direct HLS lost origin: %q", load.URI)
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, load.URI, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK {
				t.Fatalf("receiver stream: HTTP %d, %v, %s", response.StatusCode, err, body)
			}
			switch {
			case tc.transcode:
				if response.Header.Get("Content-Type") != "audio/aac" {
					t.Fatalf("receiver AAC MIME = %q", response.Header.Get("Content-Type"))
				}
				decode := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "aac", "-i", "pipe:0", "-f", "null", "-")
				decode.Stdin = bytes.NewReader(body)
				if logs, err := decode.CombinedOutput(); err != nil {
					t.Fatalf("receiver AAC invalid: %v: %s", err, logs)
				}
			case tc.hls:
				if !strings.HasPrefix(string(body), "#EXTM3U") {
					t.Fatalf("receiver HLS body = %q", body)
				}
			default:
				if !bytes.Equal(body, mp3) || response.Header.Get("Content-Type") != "audio/mpeg" {
					t.Fatal("direct MP3 changed bytes or MIME")
				}
			}
		})
	}
}

type radioDLNALoad struct {
	URI      string `xml:"CurrentURI"`
	Metadata string `xml:"CurrentURIMetaData"`
}

func radioPlaybackTestAudio(t *testing.T) (string, context.Context, []byte, []byte) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("FFmpeg unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	generate := func(format, codec string) []byte {
		t.Helper()
		data, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-c:a", codec, "-f", format, "pipe:1").Output()
		if err != nil {
			t.Fatalf("generate %s: %v", format, err)
		}
		return data
	}
	return ffmpeg, ctx, generate("mp3", "mp3"), generate("mpegts", "aac")
}

func radioPlaybackTestUpstream(t *testing.T, mp3, segment []byte) *httptest.Server {
	t.Helper()
	logo := radioTestPNG(t)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/logo.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(logo)
		case "/playlist.php":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:0.2,\nsegment.php?token=one\n#EXT-X-ENDLIST\n")
		case "/segment.php":
			if r.URL.Query().Get("token") != "one" {
				t.Error("HLS segment query lost")
			}
			w.Header().Set("Content-Type", "video/mp2t")
			_, _ = w.Write(segment)
		default:
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write(mp3)
		}
	}))
}
