package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go2tv.app/go2tv/v2/internal/controller"
)

const (
	maxMessageBytes = 64 << 10
	maxClients      = 16
	maxClientsPerIP = 4
	outboundSize    = 32
	requestIDs      = 256
	writeWait       = 15 * time.Second
	pongWait        = 60 * time.Second
	pingEvery       = 25 * time.Second
)

type commandFunc func(context.Context, envelope) controller.Result
type hubConfig struct {
	writeWait  time.Duration
	pongWait   time.Duration
	pingEvery  time.Duration
	writerGate <-chan struct{}
}

func defaultHubConfig() hubConfig {
	return hubConfig{writeWait: writeWait, pongWait: pongWait, pingEvery: pingEvery}
}

type hub struct {
	controller *controller.Controller
	command    commandFunc
	cfg        hubConfig
	mu         sync.Mutex
	clients    map[*client]struct{}
	byIP       map[string]int
	closed     bool
	stop       chan struct{}
	done       chan struct{}
	wg         sync.WaitGroup
}
type outbound struct {
	kind        string
	data        []byte
	closeCode   int
	closeReason string
}
type client struct {
	hub    *hub
	conn   *websocket.Conn
	ip     string
	send   chan outbound
	sendMu sync.Mutex
	done   chan struct{}
	once   sync.Once
	ids    map[string]struct{}
	order  []string
}

func newHub(c *controller.Controller, command commandFunc) *hub {
	return newHubWithConfig(c, command, defaultHubConfig())
}
func newHubWithConfig(c *controller.Controller, command commandFunc, cfg hubConfig) *hub {
	h := &hub{controller: c, command: command, cfg: cfg, clients: make(map[*client]struct{}), byIP: make(map[string]int), stop: make(chan struct{}), done: make(chan struct{})}
	go h.snapshots()
	return h
}
func (h *hub) close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	close(h.stop)
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		c.shutdown()
	}
	<-h.done
	h.wg.Wait()
}

var upgrader = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: func(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host)
}}

func (h *hub) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		apiError(w, 403, "request_not_allowed")
		return
	}
	h.mu.Lock()
	if h.closed || len(h.clients) >= maxClients || h.byIP[ip] >= maxClientsPerIP {
		h.mu.Unlock()
		apiError(w, http.StatusServiceUnavailable, "client_limit")
		return
	}
	h.mu.Unlock()
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &client{hub: h, conn: conn, ip: ip, send: make(chan outbound, outboundSize), done: make(chan struct{}), ids: make(map[string]struct{})}
	h.mu.Lock()
	if h.closed || len(h.clients) >= maxClients || h.byIP[ip] >= maxClientsPerIP {
		h.mu.Unlock()
		_ = conn.Close()
		return
	}
	h.clients[c] = struct{}{}
	h.byIP[ip]++
	h.wg.Add(2)
	h.mu.Unlock()
	if snapshot, err := h.controller.Snapshot(r.Context()); err == nil {
		c.enqueue("state.snapshot", mustEnvelope("state.snapshot", "", safeSnapshot(snapshot)))
	}
	go func() { defer h.wg.Done(); c.writer() }()
	go func() { defer h.wg.Done(); c.reader() }()
}
func (h *hub) remove(c *client) {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
		h.mu.Lock()
		if _, ok := h.clients[c]; ok {
			delete(h.clients, c)
			h.byIP[c.ip]--
			if h.byIP[c.ip] == 0 {
				delete(h.byIP, c.ip)
			}
		}
		h.mu.Unlock()
	})
}
func (c *client) close(code int, reason string) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	message := outbound{kind: "close", closeCode: code, closeReason: reason}
	select {
	case c.send <- message:
		return
	default:
	}
	select {
	case <-c.send:
	default:
	}
	select {
	case c.send <- message:
	default:
		c.hub.remove(c)
	}
}
func (c *client) shutdown() {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	for {
		select {
		case <-c.send:
		default:
			c.send <- outbound{kind: "server.shutdown", data: mustEnvelope("server.shutdown", "", map[string]any{})}
			c.send <- outbound{kind: "close", closeCode: websocket.CloseGoingAway, closeReason: "server shutdown"}
			return
		}
	}
}
func (c *client) enqueue(kind string, data []byte) bool {
	c.sendMu.Lock()
	select {
	case c.send <- outbound{kind: kind, data: data}:
		c.sendMu.Unlock()
		return true
	default:
	}
	if strings.HasPrefix(kind, "state.") {
		queued := make([]outbound, 0, outboundSize)
		for len(queued) < outboundSize {
			select {
			case old := <-c.send:
				queued = append(queued, old)
			default:
				goto drained
			}
		}
	drained:
		replaced := false
		for _, old := range queued {
			if old.kind == kind {
				if !replaced {
					c.send <- outbound{kind: kind, data: data}
					replaced = true
				}
				continue
			}
			c.send <- old
		}
		if replaced {
			c.sendMu.Unlock()
			return true
		}
	}
	c.sendMu.Unlock()
	c.close(websocket.ClosePolicyViolation, "slow client")
	return false
}
func (c *client) writer() {
	defer c.hub.remove(c)
	if c.hub.cfg.writerGate != nil {
		select {
		case <-c.hub.cfg.writerGate:
		case <-c.done:
			return
		}
	}
	ticker := time.NewTicker(c.hub.cfg.pingEvery)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case msg := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.hub.cfg.writeWait))
			if msg.closeCode != 0 {
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(msg.closeCode, msg.closeReason))
				return
			}
			if c.conn.WriteMessage(websocket.TextMessage, msg.data) != nil {
				c.hub.remove(c)
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.hub.cfg.writeWait))
			if c.conn.WriteMessage(websocket.PingMessage, nil) != nil {
				c.hub.remove(c)
				return
			}
		}
	}
}
func (c *client) reader() {
	c.conn.SetReadLimit(maxMessageBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(c.hub.cfg.pongWait))
	c.conn.SetPongHandler(func(string) error { return c.conn.SetReadDeadline(time.Now().Add(c.hub.cfg.pongWait)) })
	for {
		kind, data, err := c.conn.ReadMessage()
		if err != nil {
			c.hub.remove(c)
			return
		}
		if kind != websocket.TextMessage {
			c.close(websocket.CloseUnsupportedData, "text only")
			return
		}
		message, err := decodeEnvelope(data)
		if err != nil {
			c.enqueue("error", mustEnvelope("error", "", map[string]any{"code": "invalid_message"}))
			continue
		}
		if message.ProtocolVersion != ProtocolVersion {
			c.enqueue("error", mustEnvelope("error", message.ID, map[string]any{"code": "incompatible_protocol", "protocol_version": ProtocolVersion}))
			continue
		}
		if message.ID == "" || len(message.ID) > 128 {
			c.enqueue("error", mustEnvelope("error", message.ID, map[string]any{"code": "invalid_message"}))
			continue
		}
		if c.duplicate(message.ID) {
			c.enqueue("error", mustEnvelope("error", message.ID, map[string]any{"code": "duplicate_request"}))
			continue
		}
		c.enqueue("pending", mustEnvelope("pending", message.ID, map[string]any{}))
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		result := c.hub.command(ctx, message)
		cancel()
		c.enqueueResult(result)
	}
}
func (c *client) enqueueResult(result controller.Result) {
	if result.OK() {
		c.enqueue("ack", mustEnvelope("ack", result.RequestID, map[string]any{"revision": result.Revision}))
		return
	}
	c.enqueue("error", mustEnvelope("error", result.RequestID, map[string]any{"code": result.Code, "message": result.Message, "revision": result.Revision}))
}
func (c *client) duplicate(id string) bool {
	if _, ok := c.ids[id]; ok {
		return true
	}
	c.ids[id] = struct{}{}
	c.order = append(c.order, id)
	if len(c.order) > requestIDs {
		delete(c.ids, c.order[0])
		c.order = c.order[1:]
	}
	return false
}
func decodeEnvelope(data []byte) (envelope, error) {
	if excessiveNesting(data, 16) {
		return envelope{}, errors.New("nesting")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var result envelope
	if err := dec.Decode(&result); err != nil {
		return result, err
	}
	if dec.Decode(&struct{}{}) == io.EOF {
		return result, nil
	}
	return envelope{}, errors.New("trailing")
}
func excessiveNesting(data []byte, max int) bool {
	depth := 0
	quoted := false
	escaped := false
	for _, b := range data {
		if quoted {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				quoted = false
			}
			continue
		}
		if b == '"' {
			quoted = true
			continue
		}
		if b == '{' || b == '[' {
			depth++
			if depth > max {
				return true
			}
		} else if b == '}' || b == ']' {
			depth--
		}
	}
	return depth != 0 || quoted
}
func mustEnvelope(kind, id string, payload any) []byte {
	data, _ := json.Marshal(struct {
		ProtocolVersion int    `json:"protocol_version"`
		Type            string `json:"type"`
		ID              string `json:"id,omitempty"`
		Payload         any    `json:"payload"`
	}{ProtocolVersion, kind, id, payload})
	return data
}
func (h *hub) snapshots() {
	defer close(h.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var revision uint64 = ^uint64(0)
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			s, err := h.controller.Snapshot(context.Background())
			if err != nil || s.Revision == revision {
				continue
			}
			revision = s.Revision
			snapshot := safeSnapshot(s)
			h.mu.Lock()
			clients := make([]*client, 0, len(h.clients))
			for c := range h.clients {
				clients = append(clients, c)
			}
			h.mu.Unlock()
			for _, c := range clients {
				c.enqueue("state.snapshot", mustEnvelope("state.snapshot", "", snapshot))
				c.enqueue("state.devices", mustEnvelope("state.devices", "", map[string]any{"revision": snapshot.Revision, "devices": snapshot.Devices}))
				c.enqueue("state.queue", mustEnvelope("state.queue", "", map[string]any{"revision": snapshot.Revision, "queue": snapshot.Queue}))
				c.enqueue("state.playback", mustEnvelope("state.playback", "", map[string]any{"revision": snapshot.Revision, "state": snapshot.PlaybackState, "position": snapshot.Position, "duration": snapshot.Duration, "volume": snapshot.Volume, "muted": snapshot.Muted, "has_session": snapshot.HasSession}))
				c.enqueue("state.selection", mustEnvelope("state.selection", "", map[string]any{"revision": snapshot.Revision, "device_id": snapshot.SelectedDeviceID, "media": snapshot.SelectedMedia, "media_name": snapshot.SelectedMediaName, "subtitle": snapshot.SelectedSubtitle, "subtitle_name": snapshot.SelectedSubtitleName, "transcode": snapshot.Transcode, "media_type": snapshot.MediaType, "artwork_id": snapshot.ArtworkID}))
				c.enqueue("state.policy", mustEnvelope("state.policy", "", map[string]any{"revision": snapshot.Revision, "policy": snapshot.Policy}))
			}
		}
	}
}
