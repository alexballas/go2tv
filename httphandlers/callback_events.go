package httphandlers

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"go2tv.app/go2tv/v2/soapcalls"
)

const MaxCallbackBody = 256 << 10

// CallbackEvent is an immutable value handed from HTTP to playback.
type CallbackEvent struct {
	Generation     uint64
	SID            string
	SEQ            uint32
	Source         string
	TransportState string
	MediaType      string
}

type CallbackEventSink interface {
	HandleCallbackEvent(context.Context, CallbackEvent)
}

type CallbackEventSinkFunc func(context.Context, CallbackEvent)

func (f CallbackEventSinkFunc) HandleCallbackEvent(ctx context.Context, event CallbackEvent) {
	f(ctx, event)
}

type CallbackGapHandler interface {
	CallbackGap(context.Context, string, uint32, uint32)
}

type CallbackSessionConfig struct {
	Generation uint64
	SID        string
	SourceIP   net.IP
	MediaType  string
	QueueSize  int
	Sink       CallbackEventSink
	GapHandler CallbackGapHandler
}

// CallbackSession validates HTTP events and serially delivers accepted events.
type CallbackSession struct {
	sid             string
	sourceIP        net.IP
	mediaType       string
	sink            CallbackEventSink
	gap             CallbackGapHandler
	queue           chan CallbackEvent
	done            chan struct{}
	stopOnce        sync.Once
	routeGeneration uint64
	generation      atomic.Uint64
	explicitStop    atomic.Bool
}

func NewCallbackSession(cfg CallbackSessionConfig) (*CallbackSession, error) {
	if strings.TrimSpace(cfg.SID) == "" {
		return nil, errors.New("callback SID required")
	}
	if cfg.Sink == nil {
		return nil, errors.New("callback sink required")
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 32
	}
	s := &CallbackSession{
		sid: normalizeSID(cfg.SID), sourceIP: append(net.IP(nil), cfg.SourceIP...),
		mediaType: cfg.MediaType, sink: cfg.Sink, gap: cfg.GapHandler,
		queue: make(chan CallbackEvent, cfg.QueueSize), done: make(chan struct{}),
		routeGeneration: cfg.Generation,
	}
	s.generation.Store(cfg.Generation)
	go s.work()
	return s, nil
}

func (s *CallbackSession) Close()                      { s.stopOnce.Do(func() { close(s.done) }) }
func (s *CallbackSession) SetGeneration(g uint64)      { s.generation.Store(g) }
func (s *CallbackSession) SuppressExplicitStop(v bool) { s.explicitStop.Store(v) }

func (s *CallbackSession) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "NOTIFY" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if normalizeSID(r.Header.Get("SID")) != s.sid ||
			!strings.EqualFold(strings.TrimSpace(r.Header.Get("NT")), "upnp:event") ||
			!strings.EqualFold(strings.TrimSpace(r.Header.Get("NTS")), "upnp:propchange") {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		if !validCallbackContentType(r.Header.Get("Content-Type")) {
			http.Error(w, "invalid content type", http.StatusUnsupportedMediaType)
			return
		}
		seq64, err := strconv.ParseUint(strings.TrimSpace(r.Header.Get("SEQ")), 10, 32)
		if err != nil {
			http.Error(w, "invalid sequence", http.StatusBadRequest)
			return
		}
		source, err := callbackSourceIP(r.RemoteAddr)
		if err != nil || s.sourceIP != nil && !s.sourceIP.Equal(source) {
			http.Error(w, "invalid source", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxCallbackBody)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid body", http.StatusRequestEntityTooLarge)
			return
		}
		notify, err := soapcalls.ParseEventNotify(html.UnescapeString(string(body)))
		if err != nil || strings.TrimSpace(notify.TransportState) == "" {
			http.Error(w, "invalid event", http.StatusBadRequest)
			return
		}
		event := CallbackEvent{
			Generation: s.routeGeneration, SID: s.sid, SEQ: uint32(seq64),
			Source: source.String(), TransportState: notify.TransportState, MediaType: s.mediaType,
		}
		select {
		case <-s.done:
			http.Error(w, "closed", http.StatusServiceUnavailable)
		case s.queue <- event:
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "callback queue full", http.StatusServiceUnavailable)
		}
	}
}

func (s *CallbackSession) work() {
	var last uint32
	var haveLast bool
	for {
		select {
		case <-s.done:
			return
		case event := <-s.queue:
			if event.Generation != s.generation.Load() {
				continue
			}
			if haveLast {
				delta := event.SEQ - last
				switch {
				case delta == 0 || delta >= 1<<31:
					continue
				case delta != 1:
					if s.gap != nil {
						s.gap.CallbackGap(context.Background(), event.SID, last, event.SEQ)
					}
					last = event.SEQ
					continue
				}
			}
			last, haveLast = event.SEQ, true
			if s.explicitStop.Load() && event.TransportState == "STOPPED" {
				continue
			}
			s.sink.HandleCallbackEvent(context.Background(), event)
		}
	}
}

func validCallbackContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	switch strings.ToLower(mediaType) {
	case "text/xml", "application/xml":
		return true
	default:
		return false
	}
}

func callbackSourceIP(remote string) (net.IP, error) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return nil, fmt.Errorf("invalid source %q", remote)
	}
	return ip, nil
}

func normalizeSID(sid string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sid), "uuid:"))
}
