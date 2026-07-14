package playbackadapter

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/soapcalls"
)

type callbackURL string

func (u callbackURL) CallbackURL() string { return string(u) }

func TestScannerMapsPrivateEndpoints(t *testing.T) {
	scanner := Scanner{ScanFunc: func(context.Context, int) ([]devices.Device, error) {
		return []devices.Device{{Name: "TV", Addr: "http://192.0.2.2/device", Type: devices.DeviceTypeDLNA, IsAudioOnly: true}}, nil
	}}
	found, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(found) != 1 || found[0].Name != "TV" || found[0].Endpoint != "http://192.0.2.2/device" || !found[0].AudioOnly {
		t.Fatalf("devices = %#v", found)
	}
	if found[0].ID != "" {
		t.Fatalf("scanner assigned stable ID: %q", found[0].ID)
	}
}

func TestCallbackBridgeValidatedDelivery(t *testing.T) {
	bridge := NewCallbackBridge()
	defer bridge.Close()
	if err := bridge.Configure(9, "session", net.ParseIP("192.0.2.10"), "video/mp4", nil); err != nil {
		t.Fatalf("configure: %v", err)
	}
	body := `<propertyset><property><LastChange><Event><InstanceID val="0"><TransportState val="PLAYING"/></InstanceID></Event></LastChange></property></propertyset>`
	req := httptest.NewRequest("NOTIFY", "/callback", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("SID", "uuid:session")
	req.Header.Set("NT", "upnp:event")
	req.Header.Set("NTS", "upnp:propchange")
	req.Header.Set("SEQ", "1")
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	rec := httptest.NewRecorder()
	bridge.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	select {
	case event := <-bridge.Events(9):
		if event.Generation != 9 || event.TransportState != "PLAYING" {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("event timeout")
	}
}

func TestDLNAActivateCallbacksIsGenerationIdempotent(t *testing.T) {
	bridge := NewCallbackBridge()
	defer bridge.Close()
	dlna := &DLNA{
		payload:   &soapcalls.TVPayload{PinnedIP: "192.0.2.10"},
		callbacks: bridge,
		sid:       "session",
		mediaType: "video/mp4",
	}

	if err := dlna.ActivateCallbacks(7); err != nil {
		t.Fatalf("first activation: %v", err)
	}
	bridge.mu.RLock()
	first := bridge.session
	bridge.mu.RUnlock()
	if first == nil {
		t.Fatal("callback session missing")
	}

	if err := dlna.ActivateCallbacks(7); err != nil {
		t.Fatalf("repeated activation: %v", err)
	}
	bridge.mu.RLock()
	repeated := bridge.session
	bridge.mu.RUnlock()
	if repeated != first {
		t.Fatal("same generation replaced callback session")
	}

	if err := dlna.ActivateCallbacks(8); err != nil {
		t.Fatalf("new generation activation: %v", err)
	}
	bridge.mu.RLock()
	reconfigured := bridge.session
	bridge.mu.RUnlock()
	if reconfigured == first {
		t.Fatal("new generation kept callback session")
	}
}

func TestCallbackBridgeGenerationStreamsAndTerminalDelivery(t *testing.T) {
	bridge := NewCallbackBridge()
	defer bridge.Close()

	first := bridge.register(1)
	second := bridge.register(2)
	if first == second {
		t.Fatal("generations share callback stream")
	}
	bridge.emit(playback.DLNACallbackEvent{Generation: 2, TransportState: "PLAYING"})
	select {
	case event := <-bridge.Events(2):
		if event.Generation != 2 || event.TransportState != "PLAYING" {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("current generation event missing")
	}
	select {
	case event := <-first.events:
		t.Fatalf("old generation received %#v", event)
	default:
	}
	if err := bridge.Configure(2, "old-session", net.ParseIP("192.0.2.10"), "audio/mpeg", nil); err != nil {
		t.Fatalf("configure old generation: %v", err)
	}
	if err := bridge.Configure(3, "new-session", net.ParseIP("192.0.2.10"), "audio/mpeg", nil); err != nil {
		t.Fatalf("configure new generation: %v", err)
	}
	bridge.mu.RLock()
	currentSession := bridge.session
	bridge.mu.RUnlock()
	bridge.CloseGeneration(2)
	bridge.mu.RLock()
	currentAfterStaleClose := bridge.session
	bridge.mu.RUnlock()
	if currentAfterStaleClose == nil || currentAfterStaleClose != currentSession {
		t.Fatal("stale generation closed current callback session")
	}

	stream := bridge.register(3)
	for range callbackQueueSize {
		stream.events <- playback.DLNACallbackEvent{Generation: 3, TransportState: "PLAYING"}
	}
	delivered := make(chan struct{})
	go func() {
		bridge.emit(playback.DLNACallbackEvent{Generation: 3, TransportState: "STOPPED"})
		close(delivered)
	}()
	select {
	case <-delivered:
		t.Fatal("terminal event dropped from full stream")
	case <-time.After(10 * time.Millisecond):
	}
	<-stream.events
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("terminal delivery remained blocked")
	}
	found := false
	for range callbackQueueSize {
		if event := <-stream.events; event.TransportState == "STOPPED" {
			found = true
		}
	}
	if !found {
		t.Fatal("terminal event missing")
	}
}

func TestDLNARegistersCallbacksBeforeSetURIAndRecoversGapState(t *testing.T) {
	bridge := NewCallbackBridge()
	defer bridge.Close()
	callbackReady := make(chan bool, 1)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`<root><device><serviceList>
<service><serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType><serviceId>urn:upnp-org:serviceId:AVTransport</serviceId><controlURL>/transport</controlURL><eventSubURL>/events</eventSubURL></service>
</serviceList></device></root>`))
		case "SUBSCRIBE":
			w.Header().Set("SID", "uuid:session")
			w.Header().Set("TIMEOUT", "Second-300")
			w.WriteHeader(http.StatusOK)
		case "UNSUBSCRIBE":
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			action := r.Header.Get("SOAPAction")
			switch {
			case strings.Contains(action, "#SetAVTransportURI"):
				bridge.mu.RLock()
				ready := bridge.hasSession && bridge.sessionGeneration == 11 && bridge.streams[11] != nil
				bridge.mu.RUnlock()
				callbackReady <- ready
			case strings.Contains(action, "#GetTransportInfo"):
				_, _ = w.Write([]byte(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetTransportInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><CurrentTransportState>STOPPED</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus><CurrentSpeed>1</CurrentSpeed></u:GetTransportInfoResponse></s:Body></s:Envelope>`))
			}
		}
	}))
	defer server.Close()

	dlna, err := NewDLNA(context.Background(), DLNAConfig{
		Endpoint:    server.URL,
		CallbackURL: callbackURL(server.URL + "/callback"),
		Callbacks:   bridge,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = dlna.Stop(context.Background())
		_ = dlna.Close(context.Background())
	}()

	if err := dlna.ActivateCallbacks(11); err != nil {
		t.Fatalf("register callbacks: %v", err)
	}
	if bridge.Events(11) == nil {
		t.Fatal("generation stream not registered")
	}
	if err := dlna.Load(context.Background(), playback.LoadRequest{MediaURL: "http://127.0.0.1/media.mp3", MediaType: "audio/mpeg"}); err != nil {
		t.Fatalf("load: %v", err)
	}
	select {
	case ready := <-callbackReady:
		if !ready {
			t.Fatal("callbacks configured after SetAVTransportURI")
		}
	case <-time.After(time.Second):
		t.Fatal("SetAVTransportURI missing")
	}

	dlna.CallbackGap(context.Background(), "session", 1, 3)
	select {
	case event := <-bridge.Events(11):
		if event.Generation != 11 || event.TransportState != "STOPPED" {
			t.Fatalf("recovered event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("recovered STOPPED missing")
	}
}

func TestDLNALoadFreshStreamSuppressesReloadDoubleStop(t *testing.T) {
	bridge := NewCallbackBridge()
	defer bridge.Close()
	subscriptions := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`<root><device><serviceList>
<service><serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType><serviceId>urn:upnp-org:serviceId:AVTransport</serviceId><controlURL>/transport</controlURL><eventSubURL>/events</eventSubURL></service>
</serviceList></device></root>`))
		case "SUBSCRIBE":
			subscriptions++
			w.Header().Set("SID", "uuid:session-"+strconv.Itoa(subscriptions))
			w.Header().Set("TIMEOUT", "Second-300")
			w.WriteHeader(http.StatusOK)
		case "UNSUBSCRIBE", http.MethodPost:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	dlna, err := NewDLNA(context.Background(), DLNAConfig{
		Endpoint:    server.URL,
		CallbackURL: callbackURL(server.URL + "/callback"),
		Callbacks:   bridge,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dlna.Close(context.Background()) }()
	if err := dlna.ActivateCallbacks(7); err != nil {
		t.Fatal(err)
	}
	registered := bridge.currentStream(7)
	request := playback.LoadRequest{MediaURL: "http://127.0.0.1/media.mp3", MediaType: "audio/mpeg"}
	if err := dlna.Load(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	first := bridge.currentStream(7)
	if first == nil || first == registered {
		t.Fatal("first load reused callback stream")
	}
	if err := dlna.SuppressCallbackStops(7, true); err != nil {
		t.Fatal(err)
	}
	bridge.emit(playback.DLNACallbackEvent{Generation: 7, TransportState: "STOPPED"})
	select {
	case event := <-first.events:
		t.Fatalf("intentional STOPPED delivered: %#v", event)
	default:
	}
	if err := dlna.Load(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	second := bridge.currentStream(7)
	if second == nil || second == first {
		t.Fatal("reload reused callback stream")
	}
	if !second.suppressStops.Load() {
		t.Fatal("reload lost STOPPED suppression")
	}
	bridge.emit(playback.DLNACallbackEvent{Generation: 7, TransportState: "STOPPED"})
	select {
	case event := <-second.events:
		t.Fatalf("reload delivered intentional STOPPED: %#v", event)
	default:
	}
	if err := dlna.SuppressCallbackStops(7, false); err != nil {
		t.Fatal(err)
	}
	bridge.emit(playback.DLNACallbackEvent{Generation: 7, TransportState: "STOPPED"})
	select {
	case event := <-second.events:
		if event.TransportState != "STOPPED" {
			t.Fatalf("reload event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("reload STOPPED missing")
	}
	if dlna.sid != "session-2" {
		t.Fatalf("callback SID = %q", dlna.sid)
	}
}

func TestDLNACallbackGapCannotInjectIntoReloadStream(t *testing.T) {
	bridge := NewCallbackBridge()
	defer bridge.Close()
	subscriptions := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`<root><device><serviceList>
<service><serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType><serviceId>urn:upnp-org:serviceId:AVTransport</serviceId><controlURL>/transport</controlURL><eventSubURL>/events</eventSubURL></service>
</serviceList></device></root>`))
		case "SUBSCRIBE":
			subscriptions++
			w.Header().Set("SID", "uuid:session-"+strconv.Itoa(subscriptions))
			w.Header().Set("TIMEOUT", "Second-300")
			w.WriteHeader(http.StatusOK)
		case "UNSUBSCRIBE":
			w.WriteHeader(http.StatusOK)
		case http.MethodPost:
			if strings.Contains(r.Header.Get("SOAPAction"), "#GetTransportInfo") {
				_, _ = w.Write([]byte(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetTransportInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><CurrentTransportState>STOPPED</CurrentTransportState></u:GetTransportInfoResponse></s:Body></s:Envelope>`))
				return
			}
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	dlna, err := NewDLNA(context.Background(), DLNAConfig{
		Endpoint: server.URL, CallbackURL: callbackURL(server.URL + "/callback"), Callbacks: bridge,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dlna.Close(context.Background()) }()
	if err := dlna.ActivateCallbacks(12); err != nil {
		t.Fatal(err)
	}
	request := playback.LoadRequest{MediaURL: "http://127.0.0.1/media.mp3", MediaType: "audio/mpeg"}
	if err := dlna.Load(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	old := callbackGapRecovery{
		dlna: dlna, generation: 12, sid: dlna.sid,
		epoch: dlna.callbackEpoch, stream: bridge.currentStream(12),
	}
	if err := dlna.Load(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	current := bridge.currentStream(12)
	old.CallbackGap(context.Background(), old.sid, 1, 3)
	select {
	case event := <-current.events:
		t.Fatalf("stale gap injected %#v", event)
	default:
	}
	if subscriptions != 2 {
		t.Fatalf("stale gap renewed subscription: %d", subscriptions)
	}
}

func TestParseClock(t *testing.T) {
	got, err := parseClock("01:02:03.75")
	if err != nil || got != 3723 {
		t.Fatalf("parse = %d, %v", got, err)
	}
	if _, err := parseClock("bad"); err == nil {
		t.Fatal("invalid time accepted")
	}
}

func TestDLNAVolumeReadsRenderer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`<root><device><serviceList>
<service><serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType><serviceId>urn:upnp-org:serviceId:RenderingControl</serviceId><controlURL>/rendering</controlURL></service>
<service><serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType><serviceId>urn:upnp-org:serviceId:AVTransport</serviceId><controlURL>/transport</controlURL><eventSubURL>/events</eventSubURL></service>
</serviceList></device></root>`))
			return
		}
		if r.URL.Path != "/rendering" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("SOAPAction"), "#GetVolume") {
			t.Errorf("SOAPAction = %q", r.Header.Get("SOAPAction"))
		}
		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetVolumeResponse xmlns:u="urn:schemas-upnp-org:service:RenderingControl:1"><CurrentVolume>37</CurrentVolume></u:GetVolumeResponse></s:Body></s:Envelope>`))
	}))
	defer server.Close()
	transport, err := NewDLNA(context.Background(), DLNAConfig{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	volume, err := transport.Volume(context.Background())
	if err != nil || volume != 37 {
		t.Fatalf("volume = %d, err = %v", volume, err)
	}
}
