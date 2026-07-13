package webui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go2tv.app/go2tv/v2/internal/controller"
	"go2tv.app/go2tv/v2/internal/library"
	"go2tv.app/go2tv/v2/metadata"
)

func testHandler(t *testing.T) (*Handler, *library.Library, *controller.Controller, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.mp4"), []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(library.Config{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	control := controller.New(controller.Config{})
	handler, err := New(Config{Version: "test", Controller: control, Library: lib})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handler.Close(); control.Close(); _ = lib.Close() })
	return handler, lib, control, lib.Roots()[0].ID
}

func TestShellEmbedCacheAndSecurityHeaders(t *testing.T) {
	h, _, _, _ := testHandler(t)
	server := httptest.NewServer(h)
	defer server.Close()
	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, response)
	if response.Header.Get("Cache-Control") != "no-cache" || response.Header.Get("Content-Security-Policy") != csp {
		t.Fatalf("headers = %#v", response.Header)
	}
	asset := regexp.MustCompile(`/assets/(app\.[0-9a-f]{8}\.js)`).FindStringSubmatch(body)
	if len(asset) != 2 {
		t.Fatalf("hashed JS absent: %s", body)
	}
	response, err = http.Get(server.URL + "/assets/" + asset[1])
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(readBody(t, response))
	sum := sha256.Sum256(data)
	if !strings.Contains(asset[1], hex.EncodeToString(sum[:4])) {
		t.Fatalf("asset name not content hash: %s", asset[1])
	}
	if response.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatal(response.Header.Get("Cache-Control"))
	}
}

func TestStaticRejectsUnhashedAndClientUsesSafeDOM(t *testing.T) {
	h, _, _, _ := testHandler(t)
	for _, path := range []string{"/assets/index.html", "/assets/app.js", "/assets/../index.html"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s = %d %q", path, response.Code, response.Header().Get("Cache-Control"))
		}
	}
	index, err := fs.ReadFile(assets(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`app\.[0-9a-f]{8}\.js`).Find(index)
	if match == nil {
		t.Fatal("hashed script absent")
	}
	js, err := fs.ReadFile(assets(), string(match))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("innerHTML"), []byte("outerHTML"), []byte("insertAdjacentHTML")} {
		if bytes.Contains(js, forbidden) {
			t.Fatalf("unsafe DOM API %q", forbidden)
		}
	}
	if !bytes.Contains(js, []byte("textContent")) || !bytes.Contains(js, []byte("setTimeout")) {
		t.Fatal("safe labels or reconnect absent")
	}
}

func TestClientDOMInteractionsAndState(t *testing.T) {
	command := exec.Command("node", "--test", "src/client.test.js")
	command.Dir = "."
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("client tests: %v\n%s", err, output)
	}
}

func TestBootstrapAndLibrarySanitizedNoStore(t *testing.T) {
	h, _, _, rootID := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bootstrap = %d %#v", response.Code, response.Header())
	}
	if strings.Contains(response.Body.String(), `"endpoint"`) || strings.Contains(response.Body.String(), `"source"`) {
		t.Fatal("private field leak")
	}
	request = httptest.NewRequest(http.MethodGet, "/api/library?root_id="+rootID, nil)
	response = httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "movie.mp4") {
		t.Fatalf("library = %d %s", response.Code, response.Body.String())
	}
}

func TestArtworkContentAddressedCache(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "song.mp3"), []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	var artwork bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := range 20 {
		for x := range 20 {
			picture.Set(x, y, color.RGBA{R: 180, A: 255})
		}
	}
	if err := jpeg.Encode(&artwork, picture, nil); err != nil {
		t.Fatal(err)
	}
	asset, err := metadata.LoadArtwork(artwork.Bytes(), "song.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "song.jpg"), artwork.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(library.Config{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	control := controller.New(controller.Config{})
	h, err := New(Config{Version: "test", Controller: control, Library: lib})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { h.Close(); control.Close(); _ = lib.Close() }()
	rootID := lib.Roots()[0].ID
	page, err := lib.Browse(rootID, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var mediaID string
	for _, entry := range page.Entries {
		if entry.Name == "song.mp3" {
			mediaID = entry.ID
		}
	}
	result := h.command(context.Background(), envelope{Type: "library.select_media", ID: "select", Payload: json.RawMessage(`{"root_id":"` + rootID + `","entry_id":"` + mediaID + `"}`)})
	if !result.OK() {
		t.Fatal(result)
	}
	snapshot, _ := control.Snapshot(context.Background())
	if snapshot.ArtworkID == "" || strings.Contains(snapshot.ArtworkID, "song") {
		t.Fatalf("artwork ID = %q", snapshot.ArtworkID)
	}
	path := "/api/artwork/" + snapshot.ArtworkID + ".jpg"
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), asset.Data) || response.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" || response.Header().Get("ETag") == "" {
		t.Fatalf("artwork = %d %q %#v", response.Code, response.Body.Bytes(), response.Header())
	}
	conditional := httptest.NewRequest(http.MethodGet, path, nil)
	conditional.Header.Set("If-None-Match", response.Header().Get("ETag"))
	notModified := httptest.NewRecorder()
	h.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("conditional = %d %q", notModified.Code, notModified.Body.String())
	}
}

func TestStrictEnvelopeVersionUnknownAndNesting(t *testing.T) {
	valid := []byte(`{"protocol_version":1,"type":"devices.refresh","id":"1","payload":{}}`)
	if _, err := decodeEnvelope(valid); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte(`{"protocol_version":1,"type":"x","extra":1}`), []byte(`{"protocol_version":1,"type":"x"}{}`), []byte(strings.Repeat("[", 17) + strings.Repeat("]", 17))} {
		if _, err := decodeEnvelope(data); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}

func TestCommandStableQueueIDsAndStrictPayload(t *testing.T) {
	h, lib, control, rootID := testHandler(t)
	page, err := lib.Browse(rootID, "", "", 10)
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("browse: %#v %v", page, err)
	}
	entryID := page.Entries[0].ID
	command := func(kind, id, payload string) controller.Result {
		return h.command(context.Background(), envelope{ProtocolVersion: ProtocolVersion, Type: kind, ID: id, Payload: json.RawMessage(payload)})
	}
	if result := command("library.select_media", "select", `{"root_id":"`+rootID+`","entry_id":"`+entryID+`"}`); !result.OK() {
		t.Fatal(result)
	}
	if result := command("queue.add", "add1", `{"root_id":"`+rootID+`","entry_id":"`+entryID+`"}`); !result.OK() {
		t.Fatal(result)
	}
	first, _ := control.Snapshot(context.Background())
	if result := command("queue.add", "add2", `{"root_id":"`+rootID+`","entry_id":"`+entryID+`"}`); !result.OK() {
		t.Fatal(result)
	}
	second, _ := control.Snapshot(context.Background())
	if len(second.Queue) != 2 || second.Queue[0].ID != first.Queue[0].ID || second.Queue[0].ID == second.Queue[1].ID {
		t.Fatalf("unstable IDs: %#v %#v", first.Queue, second.Queue)
	}
	if result := command("queue.move", "move", `{"item_id":"`+second.Queue[1].ID+`","delta":-1}`); !result.OK() {
		t.Fatal(result)
	}
	moved, _ := control.Snapshot(context.Background())
	if moved.Queue[0].ID != second.Queue[1].ID || moved.Queue[1].ID != first.Queue[0].ID {
		t.Fatalf("move changed IDs: %#v", moved.Queue)
	}
	if result := command("player.volume", "bad", `{"volume":101}`); result.Code != controller.CodeInvalid {
		t.Fatal(result)
	}
	if result := command("player.mute", "unknown", `{"muted":true,"extra":1}`); result.Code != controller.CodeInvalid {
		t.Fatal(result)
	}
}

func TestCommandClearsSubtitle(t *testing.T) {
	h, _, control, _ := testHandler(t)
	subtitlePath := filepath.Join(t.TempDir(), "captions.srt")
	if err := os.WriteFile(subtitlePath, []byte("subtitle"), 0o600); err != nil {
		t.Fatal(err)
	}
	ref := controller.SubtitleRef{
		RootID: "root", ID: "subtitle", Name: "captions.srt",
		Open: func(context.Context) (io.ReadSeekCloser, time.Time, error) {
			file, err := os.Open(subtitlePath)
			return file, time.Time{}, err
		},
	}
	if result := control.SelectSubtitle(context.Background(), controller.Mutation{}, ref); !result.OK() {
		t.Fatal(result)
	}
	result := h.command(context.Background(), envelope{Type: "library.clear_subtitle", ID: "clear", Payload: json.RawMessage(`{}`)})
	if !result.OK() {
		t.Fatal(result)
	}
	snapshot, err := control.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SelectedSubtitle != "" {
		t.Fatalf("selected subtitle = %q", snapshot.SelectedSubtitle)
	}
	if result := h.command(context.Background(), envelope{Type: "library.clear_subtitle", ID: "bad", Payload: json.RawMessage(`{"extra":true}`)}); result.Code != controller.CodeInvalid {
		t.Fatal(result)
	}
}

func TestWebSocketProtocolDedupeAndShutdown(t *testing.T) {
	h, _, _, _ := testHandler(t)
	server := httptest.NewServer(h)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	readType(t, conn, "state.snapshot")
	if err = conn.WriteJSON(map[string]any{"protocol_version": 99, "type": "devices.refresh", "id": "bad", "payload": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	readType(t, conn, "error")
	message := map[string]any{"protocol_version": 1, "type": "devices.refresh", "id": "one", "payload": map[string]any{}}
	if err = conn.WriteJSON(message); err != nil {
		t.Fatal(err)
	}
	readType(t, conn, "pending")
	readType(t, conn, "error")
	if err = conn.WriteJSON(message); err != nil {
		t.Fatal(err)
	}
	readType(t, conn, "error")
	h.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	shutdown := false
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if closeErr, ok := err.(*websocket.CloseError); !ok || closeErr.Code != websocket.CloseGoingAway {
				t.Fatalf("shutdown close = %v", err)
			}
			break
		}
		var message struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(data, &message)
		if message.Type == "server.shutdown" {
			shutdown = true
		}
	}
	if !shutdown {
		t.Fatal("server.shutdown absent")
	}
}

func TestWebSocketPerIPLimit(t *testing.T) {
	h, _, _, _ := testHandler(t)
	server := httptest.NewServer(h)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/ws"
	connections := make([]*websocket.Conn, 0, maxClientsPerIP)
	for range maxClientsPerIP {
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
	}
	defer func() {
		for i := range connections {
			_ = connections[i].Close()
		}
	}()
	conn, response, err := websocket.DefaultDialer.Dial(url, nil)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("limit err=%v status=%v", err, response)
	}
}

func TestWebSocketGlobalLimit(t *testing.T) {
	h, _, _, _ := testHandler(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.URL.Query().Get("ip")
		r.RemoteAddr = "192.0.2." + ip + ":1234"
		h.ServeHTTP(w, r)
	}))
	defer server.Close()
	baseURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/ws?ip="
	connections := make([]*websocket.Conn, 0, maxClients)
	for i := range maxClients {
		conn, _, err := websocket.DefaultDialer.Dial(baseURL+strconv.Itoa(i/maxClientsPerIP+1), nil)
		if err != nil {
			t.Fatalf("client %d: %v", i, err)
		}
		connections = append(connections, conn)
	}
	defer func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	}()
	conn, response, err := websocket.DefaultDialer.Dial(baseURL+"5", nil)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("global limit err=%v status=%v", err, response)
	}
}

func TestWebSocketSlowClientDisconnected(t *testing.T) {
	h, _, _, _ := testHandler(t)
	h.hub.close()
	gate := make(chan struct{})
	h.hub = newHubWithConfig(h.cfg.Controller, h.command, hubConfig{
		writeWait: writeWait, pongWait: pongWait, pingEvery: pingEvery, writerGate: gate,
	})
	server := httptest.NewServer(h)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/ws", nil)
	if err != nil {
		close(gate)
		t.Fatal(err)
	}
	defer conn.Close()

	var slow *client
	deadline := time.Now().Add(2 * time.Second)
	for slow == nil && time.Now().Before(deadline) {
		h.hub.mu.Lock()
		for c := range h.hub.clients {
			slow = c
		}
		h.hub.mu.Unlock()
		if slow == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if slow == nil {
		close(gate)
		t.Fatal("client not registered")
	}
	for i := 0; i < outboundSize; i++ {
		if !slow.enqueue("toast", mustEnvelope("toast", strconv.Itoa(i), map[string]any{})) {
			break
		}
	}
	close(gate)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, err = conn.ReadMessage()
		if err == nil {
			continue
		}
		closeErr, ok := err.(*websocket.CloseError)
		if !ok || closeErr.Code != websocket.ClosePolicyViolation || closeErr.Text != "slow client" {
			t.Fatalf("slow close = %v", err)
		}
		break
	}
}

func TestWebSocketPingTimeoutDisconnects(t *testing.T) {
	h, _, _, _ := testHandler(t)
	h.hub.close()
	h.hub = newHubWithConfig(h.cfg.Controller, h.command, hubConfig{
		writeWait: 200 * time.Millisecond,
		pongWait:  120 * time.Millisecond,
		pingEvery: 20 * time.Millisecond,
	})
	server := httptest.NewServer(h)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	pingCount := 0
	conn.SetPingHandler(func(string) error {
		pingCount++
		return nil
	})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err = conn.ReadMessage(); err != nil {
			break
		}
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatalf("client deadline expired before server disconnect: %v", err)
	}
	if pingCount == 0 {
		t.Fatal("server sent no ping before timeout")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		h.hub.mu.Lock()
		clients := len(h.hub.clients)
		h.hub.mu.Unlock()
		if clients == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed-out client still registered")
}

func TestWebSocketOriginBinaryAndReadLimit(t *testing.T) {
	h, _, _, _ := testHandler(t)
	server := httptest.NewServer(h)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/ws"
	header := http.Header{"Origin": []string{"http://evil.invalid"}}
	if conn, response, err := websocket.DefaultDialer.Dial(wsURL, header); err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatalf("cross-origin upgrade: response=%v err=%v", response, err)
	}
	for _, test := range []struct {
		name string
		kind int
		data []byte
		code int
	}{
		{name: "binary", kind: websocket.BinaryMessage, data: []byte("x"), code: websocket.CloseUnsupportedData},
		{name: "too large", kind: websocket.TextMessage, data: bytes.Repeat([]byte("x"), maxMessageBytes+1), code: websocket.CloseMessageTooBig},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			readType(t, conn, "state.snapshot")
			if err = conn.WriteMessage(test.kind, test.data); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			_, _, err = conn.ReadMessage()
			closeErr, ok := err.(*websocket.CloseError)
			if !ok || closeErr.Code != test.code {
				t.Fatalf("close = %v, want %d", err, test.code)
			}
		})
	}
}

func TestOutboundStateCoalescingPreservesControl(t *testing.T) {
	c := &client{send: make(chan outbound, outboundSize)}
	c.send <- outbound{kind: "ack", data: []byte("control")}
	for range outboundSize - 1 {
		c.send <- outbound{kind: "state.snapshot", data: []byte("old")}
	}
	if !c.enqueue("state.snapshot", []byte("new")) {
		t.Fatal("state update not coalesced")
	}
	control, latest := 0, 0
	for range len(c.send) {
		message := <-c.send
		if message.kind == "ack" {
			control++
		}
		if message.kind == "state.snapshot" && string(message.data) == "new" {
			latest++
		}
	}
	if control != 1 || latest != 1 {
		t.Fatalf("control=%d latest=%d", control, latest)
	}
}

func TestCommandFailureEmitsOneTerminalMessage(t *testing.T) {
	c := &client{send: make(chan outbound, outboundSize)}
	c.enqueueResult(controller.Result{RequestID: "request", Revision: 7, Code: controller.CodeConflict, Message: "state changed"})
	if len(c.send) != 1 {
		t.Fatalf("terminal messages = %d", len(c.send))
	}
	message := <-c.send
	if message.kind != "error" || !bytes.Contains(message.data, []byte(`"id":"request"`)) || bytes.Contains(message.data, []byte(`"type":"toast"`)) {
		t.Fatalf("terminal message = %s %s", message.kind, message.data)
	}
}

func readType(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()
	for i := 0; i < 5; i++ {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var message struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &message) != nil {
			t.Fatal(string(data))
		}
		if message.Type == want {
			return
		}
	}
	t.Fatalf("missing %s", want)
}
func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
