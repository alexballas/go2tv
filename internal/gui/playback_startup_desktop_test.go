//go:build !(android || ios)

package gui

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
	"go2tv.app/go2tv/v2/devices"
)

type startupTestConn struct {
	cast.Conn
	closed chan struct{}
	once   sync.Once
}

func (*startupTestConn) Send(int, cast.Payload, string, string, string) error { return net.ErrClosed }
func (c *startupTestConn) Close() error {
	if c.closed != nil {
		c.once.Do(func() { close(c.closed) })
	}
	return nil
}

func newStartupTestCastClient(t *testing.T, addr string, conn *startupTestConn) *castprotocol.CastClient {
	t.Helper()
	client := newConnectedCastClientForTest(t, addr)
	value := reflectValueElem(t, client)
	app := reflectNewAtField(value.FieldByName("app")).Elem()
	reflectNewAtField(app.FieldByName("conn")).Set(reflect.ValueOf(conn))
	reflectNewAtField(value.FieldByName("conn")).Set(reflect.ValueOf(conn))
	return client
}

func TestStopInterruptsChromecastLoad(t *testing.T) {
	s, _ := newMediaCardTestScreen(t)
	s.State, s.mediafile = "Stopped", "movie.mp4"
	s.playbackStatus = newPlaybackStatusLabel("")
	s.Stop = widget.NewButton("Stop", func() { stopAction(s) })
	s.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
	s.CurrentPos, s.EndPos = binding.NewString(), binding.NewString()
	s.selectedDevice = devType{addr: "http://living-room:8009", deviceType: devices.DeviceTypeChromecast}
	s.activeDevice = s.selectedDevice
	conn := &startupTestConn{closed: make(chan struct{})}
	s.chromecastClient = newStartupTestCastClient(t, s.selectedDevice.addr, conn)
	t.Cleanup(func() { _ = conn.Close(); stopActionSync(s) })
	_, finish, starting := s.beginPlaybackStartup(s.mediafile)
	if !starting {
		t.Fatal("startup rejected")
	}
	actionID := s.nextChromecastActionID()
	entered, result := make(chan struct{}), make(chan error, 1)
	go func() {
		defer finish()
		_, err := loadChromecastForActionWith(s, actionID, s.chromecastClient, castprotocol.LoadRequest{},
			func() (*castprotocol.CastClient, error) {
				t.Error("cancelled LOAD reconnected")
				return nil, context.Canceled
			},
			func(*castprotocol.CastClient, castprotocol.LoadRequest) error {
				close(entered)
				<-conn.closed
				return net.ErrClosed
			})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("LOAD did not start")
	}
	setPlayPauseView("", s)
	fyne.DoAndWait(func() { test.Tap(s.Stop) })
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("LOAD cancellation = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop waited for LOAD to time out")
	}
	if done := s.torrentCancellationDone(); done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Stop did not finish LOAD cleanup")
		}
	}
	if s.chromecastClient != nil || s.getScreenState() != "Stopped" || s.PlayPause.Disabled() {
		t.Fatal("cancelled LOAD retained the cast")
	}
}

func TestURLPlaybackSurvivesStartupCompletion(t *testing.T) {
	s, _ := newMediaCardTestScreen(t)
	s.State = "Stopped"
	s.playbackStatus = newPlaybackStatusLabel("")
	s.Stop = widget.NewButton("Stop", func() { stopAction(s) })
	s.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
	s.CurrentPos, s.EndPos = binding.NewString(), binding.NewString()
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "SUBSCRIBE" {
			w.Header().Set("SID", "uuid:stream")
			w.Header().Set("TIMEOUT", "Second-300")
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(renderer.Close)
	s.controlURL, s.eventURL = renderer.URL, renderer.URL
	s.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
	release := make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = io.WriteString(w, "second")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { unblock(); media.Close() })
	t.Cleanup(func() { stopActionSync(s) })
	s.ExternalMediaURL.SetChecked(true)
	s.MediaText.SetText(media.URL + "/movie.mp4")
	playAction(s)
	s.torrent.mu.Lock()
	startup := s.torrent.startup
	s.torrent.mu.Unlock()
	if startup != nil {
		select {
		case <-startup.done:
		case <-time.After(5 * time.Second):
			t.Fatal("URL cast startup blocked")
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.playbackStartupPending() {
		if time.Now().After(deadline) {
			t.Fatal("URL cast startup blocked")
		}
		time.Sleep(time.Millisecond)
	}
	if s.tvdata == nil || s.httpserver == nil {
		t.Fatal("URL cast did not start")
	}
	// The renderer may take time to send its Playing callback after accepting
	// the media. Stop must remain available throughout that wait.
	fyne.DoAndWait(func() {
		s.refreshPlaybackReadiness()
		if s.Stop.Disabled() {
			t.Fatal("Stop disabled while waiting for playback confirmation")
		}
	})
	unblock()
	response, err := http.Get(s.tvdata.MediaURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "firstsecond" {
		t.Fatalf("URL playback truncated after startup: body=%q, err=%v", body, err)
	}
}

func TestDLNAStatusFallbackAfterStartup(t *testing.T) {
	tt := []struct {
		name, transportState, screenState, button, action string
	}{
		{"playing", "PLAYING", "Playing", "Pause", "Pause"},
		{"paused", "PAUSED_PLAYBACK", "Paused", "Play", "Play"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newMediaCardTestScreen(t)
			s.State = "Stopped"
			s.ffmpegPath = filepath.Join(t.TempDir(), "missing-ffmpeg")
			s.playbackStatus = newPlaybackStatusLabel("")
			s.Stop = widget.NewButton("Stop", func() { stopAction(s) })
			s.PlayPause.OnTapped = func() { go playAction(s) }
			s.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
			s.CurrentPos, s.EndPos = binding.NewString(), binding.NewString()
			var loads atomic.Int32
			actions := make(chan string, 4)
			queried := make(chan struct{})
			signalQueried := sync.OnceFunc(func() { close(queried) })
			renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if r.Method == "SUBSCRIBE" {
					w.Header().Set("SID", "uuid:fallback")
					w.Header().Set("TIMEOUT", "Second-300")
				}
				switch action := r.Header.Get("SOAPAction"); {
				case strings.Contains(action, "#SetAVTransportURI\""):
					loads.Add(1)
				case strings.Contains(action, "#GetTransportInfo\""):
					_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetTransportInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><CurrentTransportState>`+tc.transportState+`</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus><CurrentSpeed>1</CurrentSpeed></u:GetTransportInfoResponse></s:Body></s:Envelope>`)
					signalQueried()
					return
				case strings.Contains(action, "#Play\""):
					actions <- "Play"
				case strings.Contains(action, "#Pause\""):
					actions <- "Pause"
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(renderer.Close)
			s.controlURL, s.eventURL = renderer.URL, renderer.URL
			s.selectedDevice = devType{addr: renderer.URL, deviceType: devices.DeviceTypeDLNA}
			media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "video/mp4")
				_, _ = io.WriteString(w, "video")
			}))
			t.Cleanup(media.Close)
			t.Cleanup(func() { stopActionSync(s) })
			s.ExternalMediaURL.SetChecked(true)
			s.MediaText.SetText(media.URL + "/movie.mp4")
			playAction(s)
			s.torrent.mu.Lock()
			startup := s.torrent.startup
			s.torrent.mu.Unlock()
			if startup != nil {
				select {
				case <-startup.done:
				case <-time.After(2 * time.Second):
					t.Fatal("startup did not finish before fallback timer")
				}
			}
			select {
			case action := <-actions:
				if action != "Play" {
					t.Fatalf("startup action = %q", action)
				}
			case <-time.After(time.Second):
				t.Fatal("startup did not send Play")
			}
			// This renderer never sends callbacks. The fallback must still
			// discover playback after media preparation has finished.
			select {
			case <-queried:
			case <-time.After(5 * time.Second):
				t.Fatal("startup completion cancelled DLNA status fallback")
			}
			deadline := time.Now().Add(time.Second)
			for {
				ready := false
				fyne.DoAndWait(func() {
					ready = s.getScreenState() == tc.screenState && strings.TrimSpace(s.PlayPause.Text) == tc.button && !s.PlayPause.Disabled()
				})
				if ready {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("fallback did not restore %s controls", tc.screenState)
				}
				time.Sleep(time.Millisecond)
			}
			fyne.DoAndWait(func() { test.Tap(s.PlayPause) })
			select {
			case action := <-actions:
				if action != tc.action || loads.Load() != 1 {
					t.Fatalf("button action = %q, loads = %d; want %q without reload", action, loads.Load(), tc.action)
				}
			case <-time.After(time.Second):
				t.Fatalf("button did not send %s", tc.action)
			}
		})
	}
}

func TestStopDuringMediaPreparation(t *testing.T) {
	tt := []struct {
		name, source, deviceType string
	}{
		{"DLNA torrent", "torrent", devices.DeviceTypeDLNA},
		{"Chromecast torrent", "torrent", devices.DeviceTypeChromecast},
		{"DLNA local probe", "local", devices.DeviceTypeDLNA},
		{"Chromecast local probe", "local", devices.DeviceTypeChromecast},
		{"DLNA URL request", "URL", devices.DeviceTypeDLNA},
		{"Chromecast URL request", "URL", devices.DeviceTypeChromecast},
		{"DLNA renderer loading", "renderer", devices.DeviceTypeDLNA},
		{"DLNA embedded probe", "embedded", devices.DeviceTypeDLNA},
		{"Chromecast embedded probe", "embedded", devices.DeviceTypeChromecast},
		{"DLNA subtitle extraction", "extraction", devices.DeviceTypeDLNA},
		{"Chromecast subtitle extraction", "extraction", devices.DeviceTypeChromecast},
		{"DLNA native subtitle probe", "native", devices.DeviceTypeDLNA},
		{"Chromecast native subtitle probe", "native", devices.DeviceTypeChromecast},
		{"DLNA subtitle conversion", "conversion", devices.DeviceTypeDLNA},
		{"Chromecast subtitle conversion", "conversion", devices.DeviceTypeChromecast},
		{"DLNA direct duration probe", "duration", devices.DeviceTypeDLNA},
		{"DLNA FFmpeg validation", "validation", devices.DeviceTypeDLNA},
		{"Chromecast FFmpeg validation", "validation", devices.DeviceTypeChromecast},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newMediaCardTestScreen(t)
			s.State = "Stopped"
			s.Transcode = true
			s.playbackStatus = newPlaybackStatusLabel("")
			s.Stop = widget.NewButton("Stop", func() { stopAction(s) })
			s.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
			s.CurrentPos, s.EndPos = binding.NewString(), binding.NewString()

			entered := make(chan struct{})
			signalEntered := sync.OnceFunc(func() { close(entered) })
			var plays atomic.Int32
			renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				if r.Method == "SUBSCRIBE" {
					w.Header().Set("SID", "uuid:startup")
					w.Header().Set("TIMEOUT", "Second-300")
				}
				if strings.Contains(r.Header.Get("SOAPAction"), "#Play\"") {
					plays.Add(1)
				}
				if tc.source == "renderer" && strings.Contains(r.Header.Get("SOAPAction"), "#SetAVTransportURI\"") {
					signalEntered()
					<-r.Context().Done()
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(renderer.Close)
			s.controlURL, s.eventURL = renderer.URL, renderer.URL
			s.selectedDevice = devType{addr: renderer.URL, name: "Living Room", deviceType: tc.deviceType}
			s.selectedDeviceType = tc.deviceType
			if tc.deviceType == devices.DeviceTypeChromecast {
				s.chromecastClient = newStartupTestCastClient(t, renderer.URL, &startupTestConn{})
			}
			t.Cleanup(func() { stopActionSync(s) })

			switch tc.source {
			case "torrent", "local", "embedded", "extraction", "native", "conversion", "duration", "validation":
				if tc.source == "torrent" {
					session, path := newTestTorrentSession(t)
					s.torrent.session, s.torrent.path, s.mediafile = session, path, path
				} else {
					s.mediafile = filepath.Join(t.TempDir(), "movie.mp4")
					if err := os.WriteFile(s.mediafile, []byte("\x00\x00\x00\x18ftypisom\x00\x00\x00\x00isommp42"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				var probeEntered string
				s.ffmpegPath, probeEntered, _ = torrentTestBlockedProbe(t)
				switch tc.source {
				case "embedded", "extraction", "native":
					s.SelectInternalSubs.Options = []string{"English"}
					s.SelectInternalSubs.SetSelected("English")
					if tc.source == "embedded" {
						fyne.CurrentApp().Preferences().SetBool(chromecastBurnSubtitlesPref, true)
					} else {
						s.Transcode = false
					}
					if tc.source == "native" {
						nativePath := filepath.Join(filepath.Dir(s.mediafile), "movie.mkv")
						if err := os.Rename(s.mediafile, nativePath); err != nil {
							t.Fatal(err)
						}
						s.mediafile = nativePath
					}
				case "conversion":
					s.Transcode = false
					s.subsfile = filepath.Join(filepath.Dir(s.mediafile), "movie.ass")
					if err := os.WriteFile(s.subsfile, []byte("[Script Info]\nScriptType: v4.00+\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Hello\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "duration":
					s.Transcode = false
				case "validation":
					fyne.CurrentApp().Preferences().SetBool(chromecastBurnSubtitlesPref, true)
				}
				if tc.source == "extraction" || tc.source == "conversion" || tc.source == "validation" {
					script := `#!/bin/sh
: > "$GO2TV_TEST_PROBE_ENTERED"
while [ ! -e "$GO2TV_TEST_PROBE_RELEASE" ]; do sleep 0.01; done
`
					if tc.source != "validation" {
						script = strings.Replace(script, "#!/bin/sh\n", "#!/bin/sh\nif [ \"$1\" = \"-h\" ]; then exit 0; fi\n", 1)
					}
					if err := os.WriteFile(s.ffmpegPath, []byte(script), 0o700); err != nil {
						t.Fatal(err)
					}
				}
				playAction(s)
				waitForTorrentTestProbe(t, probeEntered)
			case "URL":
				media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					signalEntered()
					<-r.Context().Done()
				}))
				t.Cleanup(media.Close)
				s.ExternalMediaURL.SetChecked(true)
				s.MediaText.SetText(media.URL + "/movie.mp4")
				playAction(s)
			case "renderer":
				s.Transcode = false
				s.mediafile = torrentTestReplacementImage(t)
				playAction(s)
			}
			if tc.source == "URL" || tc.source == "renderer" {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("startup did not reach the stalled request")
				}
			}
			fyne.DoAndWait(func() {
				s.refreshPlaybackReadiness()
				if s.Stop.Disabled() || !s.PlayPause.Disabled() || !strings.HasPrefix(s.playbackStatus.Text, "Loading media") {
					t.Fatal("pending cast must offer Stop and disable Cast")
				}
				// Duplicate requests must not launch another startup worker.
				playAction(s)
				test.Tap(s.Stop)
			})
			if done := s.torrentCancellationDone(); done != nil {
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("Stop did not interrupt media preparation")
				}
			}
			fyne.DoAndWait(func() {
				if s.PlayPause.Disabled() || !s.Stop.Disabled() || !strings.HasPrefix(s.playbackStatus.Text, "Ready to cast") {
					t.Fatal("Stop must restore Cast without changing device selection")
				}
			})
			if plays.Load() != 0 || s.tvdata != nil || s.httpserver != nil || s.getActiveDevice().addr != "" || s.getScreenState() != "Stopped" {
				t.Fatal("cancelled startup left playback running")
			}
			if s.selectedDevice.addr != renderer.URL || s.hasTorrentSession() != (tc.source == "torrent") {
				t.Fatal("Stop changed device selection or cancelled the download")
			}
		})
	}
}
