//go:build !(android || ios)

package gui

import (
	"bytes"
	"encoding/json"
	"image"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/data/binding"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/castprotocol/v2/application"
	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
	pb "go2tv.app/go2tv/v2/castprotocol/v2/cast/proto"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/internal/radiobrowser"
)

func TestRadioChromecastPlaybackFromSelection(t *testing.T) {
	ffmpeg, ctx, mp3, segment := radioPlaybackTestAudio(t)
	tt := []struct {
		name      string
		hls       bool
		transcode bool
		artwork   bool
	}{
		{name: "direct MP3 with artwork", artwork: true},
		{name: "transcoded MP3 with artwork", transcode: true, artwork: true},
		{name: "direct HLS", hls: true},
		{name: "transcoded HLS", hls: true, transcode: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			upstream := radioPlaybackTestUpstream(t, mp3, segment)
			defer upstream.Close()
			station := radiobrowser.Station{UUID: "station", Name: "Radio & Music", URL: upstream.URL + "/radio.php?token=one", Codec: "MP3"}
			if tc.hls {
				station.URL, station.Codec, station.HLS = upstream.URL+"/playlist.php?token=one", "AAC", 1
			}
			if tc.artwork {
				station.Favicon = upstream.URL + "/logo.png"
			}
			screen, card := newMediaCardTestScreen(t)
			screen.Current = fyne.CurrentApp().NewWindow("Radio cast")
			defer screen.Current.Close()
			screen.Current.SetContent(card.content)
			screen.SlideBar = &tappedSlider{Slider: widget.NewSlider(0, 100)}
			screen.CurrentPos, screen.EndPos = binding.NewString(), binding.NewString()
			screen.ffmpegPath = ffmpeg
			screen.TranscodeCheckBox = widget.NewCheck("Transcode", func(value bool) { screen.Transcode = value })
			screen.TranscodeCheckBox.SetChecked(tc.transcode)
			screen.selectedDevice = devType{addr: "http://127.0.0.1:8009", deviceType: devices.DeviceTypeChromecast, isAudioOnly: true}
			screen.selectedDeviceType = devices.DeviceTypeChromecast
			radioWindow(ctx, screen)
			r := screen.radio
			r.client = radioTestDirectory{}
			fyne.DoAndWait(func() {
				r.setStations([]radiobrowser.Station{station})
				r.list.Select(0)
				test.Tap(r.useStation)
			})
			waitRadioUI(t, func() bool { return screen.MediaText.Text == station.StreamURL() })
			conn := &radioCastReceiver{messages: make(chan *pb.CastMessage, 8), loaded: make(chan cast.MediaItem, 1)}
			app := application.NewApplication(application.WithConnection(conn), application.WithConnectionRetries(1))
			client := newConnectedCastClientForTest(t, screen.selectedDevice.addr)
			value := reflectValueElem(t, client)
			reflectNewAtField(value.FieldByName("conn")).Set(reflect.ValueOf(conn))
			reflectNewAtField(value.FieldByName("app")).Set(reflect.ValueOf(app))
			screen.chromecastClient = client
			defer func() { stopActionSync(screen); _ = app.Close(false) }()
			playAction(screen)
			var media cast.MediaItem
			select {
			case media = <-conn.loaded:
			case <-ctx.Done():
				t.Fatal("station did not reach Chromecast LOAD: ", ctx.Err())
			}
			if media.Metadata.Title != station.Name || media.StreamType != "LIVE" {
				t.Fatalf("receiver metadata: title=%q, stream=%q", media.Metadata.Title, media.StreamType)
			}
			if media.Metadata.MetadataType != 3 {
				t.Fatalf("receiver classified radio metadata as %d, want music", media.Metadata.MetadataType)
			}
			if tc.artwork {
				if len(media.Metadata.Images) != 1 {
					t.Fatalf("receiver station artwork count = %d", len(media.Metadata.Images))
				}
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, media.Metadata.Images[0].URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				config, _, decodeErr := image.DecodeConfig(response.Body)
				_ = response.Body.Close()
				if decodeErr != nil || config.Width != 24 || config.Height != 16 || response.Header.Get("Content-Type") != "image/jpeg" {
					t.Fatalf("receiver station artwork unavailable: %v, %+v, %v", decodeErr, config, response.Header)
				}
			}
			wantType := "audio/mpeg"
			if tc.hls {
				wantType = "application/vnd.apple.mpegurl"
			}
			if tc.transcode {
				wantType = "audio/aac"
			}
			if media.ContentType != wantType {
				t.Fatalf("Chromecast MIME = %q, want %q", media.ContentType, wantType)
			}
			if !tc.transcode && media.ContentId != station.StreamURL() {
				t.Fatalf("direct cast lost station origin: %q", media.ContentId)
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, media.ContentId, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != wantType {
				t.Fatalf("receiver stream: HTTP %d, MIME %q, %v", response.StatusCode, response.Header.Get("Content-Type"), err)
			}
			if tc.transcode {
				decode := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "aac", "-i", "pipe:0", "-f", "null", "-")
				decode.Stdin = bytes.NewReader(body)
				if logs, err := decode.CombinedOutput(); err != nil {
					t.Fatalf("receiver AAC invalid: %v: %s", err, logs)
				}
			}
		})
	}
}

func TestRadioChromecastSwitchReusesReceiver(t *testing.T) {
	tt := []struct {
		name      string
		reachable bool
	}{
		{name: "next station loads on running receiver", reachable: true},
		{name: "failed switch stops previous station"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "audio/mpeg")
				_, _ = io.WriteString(w, "audio")
			}))
			defer upstream.Close()
			unreachable := httptest.NewServer(http.NotFoundHandler())
			unreachable.Close()
			first := radiobrowser.Station{UUID: "first", Name: "First", URL: upstream.URL + "/first"}
			next := radiobrowser.Station{UUID: "next", Name: "Next", URL: upstream.URL + "/next"}
			if !tc.reachable {
				next.URL = unreachable.URL + "/next"
			}
			screen, card := newRadioPlaybackTestScreen(t)
			screen.selectedDevice = devType{addr: "http://127.0.0.1:8009", deviceType: devices.DeviceTypeChromecast, isAudioOnly: true}
			screen.selectedDeviceType = devices.DeviceTypeChromecast
			radioWindow(t.Context(), screen)
			r := screen.radio
			r.client = radioTestDirectory{stations: []radiobrowser.Station{first, next}}
			conn := &radioCastReceiver{messages: make(chan *pb.CastMessage, 8), loaded: make(chan cast.MediaItem, 4)}
			app := application.NewApplication(application.WithConnection(conn), application.WithConnectionRetries(1))
			client := newConnectedCastClientForTest(t, screen.selectedDevice.addr)
			value := reflectValueElem(t, client)
			reflectNewAtField(value.FieldByName("conn")).Set(reflect.ValueOf(conn))
			reflectNewAtField(value.FieldByName("app")).Set(reflect.ValueOf(app))
			screen.chromecastClient = client
			defer func() { stopActionSync(screen); _ = app.Close(false) }()
			load := func(station radiobrowser.Station) cast.MediaItem {
				t.Helper()
				select {
				case media := <-conn.loaded:
					return media
				case <-time.After(5 * time.Second):
					t.Fatalf("%s did not reach Chromecast LOAD", station.Name)
				}
				return cast.MediaItem{}
			}
			use := func(index int) {
				fyne.DoAndWait(func() { test.Tap(card.radio) })
				waitRadioUI(t, func() bool { return r.list.Length() == 2 })
				fyne.DoAndWait(func() { r.list.Select(index); test.Tap(r.useStation) })
			}
			use(0)
			waitRadioUI(t, func() bool { return screen.MediaText.Text == first.StreamURL() && r.modal == nil })
			playAction(screen)
			load(first)
			waitUI(t, 5*time.Second, func() bool { return screen.getScreenState() == "Playing" })
			use(1)
			if !tc.reachable {
				waitUI(t, 5*time.Second, func() bool { return screen.getScreenState() == "Stopped" && screen.chromecastClient == nil })
				conn.mu.Lock()
				stopped, closed := conn.stopped, conn.closed
				conn.mu.Unlock()
				if !stopped || !closed {
					t.Fatalf("previous station left on receiver: stopped=%v closed=%v", stopped, closed)
				}
				return
			}
			if media := load(next); media.ContentId != next.StreamURL() {
				t.Fatalf("switch loaded %q, want %q", media.ContentId, next.StreamURL())
			}
			waitUI(t, 5*time.Second, func() bool { return screen.getScreenState() == "Playing" })
			conn.mu.Lock()
			stopped, closed := conn.stopped, conn.closed
			conn.mu.Unlock()
			if stopped || closed || screen.chromecastClient != client {
				t.Fatalf("switch relaunched receiver: stopped=%v closed=%v sameClient=%v", stopped, closed, screen.chromecastClient == client)
			}
		})
	}
}

// A software receiver acknowledges control/status requests. Media still passes
// through the GUI's production casting path and real HTTP/FFmpeg processing.
type radioCastReceiver struct {
	mu       sync.Mutex
	messages chan *pb.CastMessage
	loaded   chan cast.MediaItem
	media    cast.MediaItem
	session  int
	stopped  bool // receiver app STOP, which quits the receiver
	closed   bool
}

func (c *radioCastReceiver) Start(string, int) error       { return nil }
func (c *radioCastReceiver) SetDebug(bool)                 {}
func (c *radioCastReceiver) MsgChan() chan *pb.CastMessage { return c.messages }
func (c *radioCastReceiver) LocalAddr() (string, error)    { return "127.0.0.1", nil }
func (c *radioCastReceiver) RemoteAddr() (string, error)   { return "127.0.0.1", nil }
func (c *radioCastReceiver) RemotePort() (string, error)   { return "8009", nil }
func (c *radioCastReceiver) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *radioCastReceiver) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed
}

func (c *radioCastReceiver) Send(id int, payload cast.Payload, source, destination, namespace string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	payload.SetRequestId(id)
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var command cast.LoadMediaCommand
	if err := json.Unmarshal(data, &command); err != nil {
		return err
	}
	switch command.Type {
	case "LOAD":
		c.media = command.Media
		c.session++
		c.loaded <- command.Media
	case "STOP":
		if strings.HasSuffix(namespace, ".receiver") {
			c.stopped = true
		}
	case "GET_STATUS":
		var response any
		if strings.HasSuffix(namespace, ".media") {
			state := "IDLE"
			if c.media.ContentId != "" {
				state = "PLAYING"
			}
			response = map[string]any{"type": "MEDIA_STATUS", "requestId": id, "status": []any{map[string]any{"mediaSessionId": c.session, "playerState": state, "currentTime": 0, "media": c.media, "volume": map[string]any{"level": 0.5, "muted": false}}}}
		} else {
			response = map[string]any{"type": "RECEIVER_STATUS", "requestId": id, "status": map[string]any{"applications": []any{map[string]any{"appId": cast.DefaultMediaReceiverAppID, "transportId": "radio", "sessionId": "radio"}}, "volume": map[string]any{"level": 0.5, "muted": false}}}
		}
		body, err := json.Marshal(response)
		if err != nil {
			return err
		}
		message := string(body)
		c.messages <- &pb.CastMessage{PayloadUtf8: &message, Namespace: &namespace, SourceId: &destination, DestinationId: &source}
	}
	return nil
}
