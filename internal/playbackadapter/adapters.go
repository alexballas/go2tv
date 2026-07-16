package playbackadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/soapcalls"
)

const callbackQueueSize = 128

// Scanner adapts fresh legacy protocol discovery to playback discovery.
type Scanner struct {
	DLNADelay int
	ScanFunc  func(context.Context, int) ([]devices.Device, error)
}

func (s Scanner) Scan(ctx context.Context) ([]playback.Device, error) {
	scan := s.ScanFunc
	if scan == nil {
		scan = devices.ScanAllDevices
	}
	found, err := scan(ctx, s.DLNADelay)
	if err != nil {
		return nil, err
	}
	result := make([]playback.Device, 0, len(found))
	for _, device := range found {
		result = append(result, playback.Device{
			Name: device.Name, Protocol: device.Type, AudioOnly: device.IsAudioOnly,
			Endpoint: device.Addr,
		})
	}
	return result, nil
}

type callbackStream struct {
	events        chan playback.DLNACallbackEvent
	done          chan struct{}
	stopOnce      sync.Once
	suppressStops atomic.Bool
}

func newCallbackStream() *callbackStream {
	return &callbackStream{
		events: make(chan playback.DLNACallbackEvent, callbackQueueSize),
		done:   make(chan struct{}),
	}
}

func (s *callbackStream) close() { s.stopOnce.Do(func() { close(s.done) }) }

func (s *callbackStream) deliver(event playback.DLNACallbackEvent) {
	if strings.EqualFold(strings.TrimSpace(event.TransportState), "STOPPED") {
		if s.suppressStops.Load() {
			return
		}
		select {
		case s.events <- event:
		case <-s.done:
		}
		return
	}
	select {
	case s.events <- event:
	case <-s.done:
	default:
	}
}

// CallbackBridge is a stable HTTP callback target with generation-isolated streams.
type CallbackBridge struct {
	mu                sync.RWMutex
	session           *httphandlers.CallbackSession
	sessionGeneration uint64
	hasSession        bool
	activeGeneration  uint64
	hasActive         bool
	streams           map[uint64]*callbackStream
}

func NewCallbackBridge() *CallbackBridge {
	return &CallbackBridge{streams: make(map[uint64]*callbackStream)}
}

func (b *CallbackBridge) register(generation uint64) *callbackStream {
	b.mu.Lock()
	if b.hasActive && b.activeGeneration == generation {
		stream := b.streams[generation]
		if stream == nil {
			stream = newCallbackStream()
			b.streams[generation] = stream
		}
		b.mu.Unlock()
		return stream
	}

	oldSession := b.session
	b.session = nil
	b.hasSession = false
	for oldGeneration, stream := range b.streams {
		if oldGeneration == generation {
			continue
		}
		stream.close()
		delete(b.streams, oldGeneration)
	}
	stream := b.streams[generation]
	if stream == nil {
		stream = newCallbackStream()
		b.streams[generation] = stream
	}
	b.activeGeneration = generation
	b.hasActive = true
	b.mu.Unlock()

	if oldSession != nil {
		oldSession.Close()
	}
	return stream
}

func (b *CallbackBridge) replaceStream(generation uint64) (*callbackStream, error) {
	b.mu.Lock()
	if b.hasActive && b.activeGeneration != generation {
		b.mu.Unlock()
		return nil, errors.New("callback generation superseded")
	}
	oldStream := b.streams[generation]
	stream := newCallbackStream()
	if oldStream != nil {
		stream.suppressStops.Store(oldStream.suppressStops.Load())
	}
	b.streams[generation] = stream
	b.activeGeneration = generation
	b.hasActive = true
	var oldSession *httphandlers.CallbackSession
	if b.hasSession && b.sessionGeneration == generation {
		oldSession = b.session
		b.session = nil
		b.hasSession = false
	}
	b.mu.Unlock()
	if oldStream != nil {
		oldStream.close()
	}
	if oldSession != nil {
		oldSession.Close()
	}
	return stream, nil
}

func (b *CallbackBridge) currentStream(generation uint64) *callbackStream {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.hasActive || b.activeGeneration != generation {
		return nil
	}
	return b.streams[generation]
}

// SuppressCallbackStops controls STOPPED delivery for one callback generation.
func (b *CallbackBridge) SuppressCallbackStops(generation uint64, suppress bool) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.hasActive || b.activeGeneration != generation || b.streams[generation] == nil {
		return errors.New("callback generation unavailable")
	}
	b.streams[generation].suppressStops.Store(suppress)
	return nil
}

func (b *CallbackBridge) Events(generation uint64) <-chan playback.DLNACallbackEvent {
	b.mu.RLock()
	stream := b.streams[generation]
	b.mu.RUnlock()
	if stream == nil {
		return nil
	}
	return stream.events
}

func (b *CallbackBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.RLock()
	session := b.session
	b.mu.RUnlock()
	if session == nil {
		http.Error(w, "callback unavailable", http.StatusServiceUnavailable)
		return
	}
	session.Handler().ServeHTTP(w, r)
}

func (b *CallbackBridge) Configure(generation uint64, sid string, source net.IP, mediaType string, gap httphandlers.CallbackGapHandler) error {
	return b.configure(generation, sid, source, mediaType, func(*callbackStream) httphandlers.CallbackGapHandler { return gap })
}

func (b *CallbackBridge) configure(generation uint64, sid string, source net.IP, mediaType string, gap func(*callbackStream) httphandlers.CallbackGapHandler) error {
	stream := b.register(generation)
	session, err := httphandlers.NewCallbackSession(httphandlers.CallbackSessionConfig{
		Generation: generation, SID: sid, SourceIP: source, MediaType: mediaType,
		QueueSize: callbackQueueSize,
		Sink: httphandlers.CallbackEventSinkFunc(func(_ context.Context, event httphandlers.CallbackEvent) {
			stream.deliver(playback.DLNACallbackEvent{Generation: event.Generation, TransportState: event.TransportState})
		}),
		GapHandler: gap(stream),
	})
	if err != nil {
		return err
	}
	b.mu.Lock()
	if !b.hasActive || b.activeGeneration != generation || b.streams[generation] != stream {
		b.mu.Unlock()
		session.Close()
		return errors.New("callback generation superseded")
	}
	old := b.session
	b.session = session
	b.sessionGeneration = generation
	b.hasSession = true
	b.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}

func (b *CallbackBridge) CloseGeneration(generation uint64) {
	b.mu.Lock()
	stream := b.streams[generation]
	delete(b.streams, generation)
	var session *httphandlers.CallbackSession
	if b.hasSession && b.sessionGeneration == generation {
		session = b.session
		b.session = nil
		b.hasSession = false
	}
	if b.hasActive && b.activeGeneration == generation {
		b.hasActive = false
	}
	b.mu.Unlock()
	if stream != nil {
		stream.close()
	}
	if session != nil {
		session.Close()
	}
}

func (b *CallbackBridge) Close() {
	b.mu.Lock()
	session := b.session
	b.session = nil
	b.hasSession = false
	b.hasActive = false
	streams := b.streams
	b.streams = make(map[uint64]*callbackStream)
	b.mu.Unlock()
	for _, stream := range streams {
		stream.close()
	}
	if session != nil {
		session.Close()
	}
}

type CallbackURLProvider interface {
	CallbackURL() string
}

type DLNAConfig struct {
	Endpoint    string
	LogOutput   io.Writer
	CallbackURL CallbackURLProvider
	Callbacks   *CallbackBridge
}

// DLNA preserves TVPayload SOAP fallback/logging while serializing contextual calls.
type DLNA struct {
	mu                 sync.Mutex
	payload            *soapcalls.TVPayload
	callbackURL        CallbackURLProvider
	callbacks          *CallbackBridge
	sid                string
	mediaType          string
	callbackGeneration uint64
	callbacksRequested bool
	callbacksActive    bool
	callbackEpoch      uint64
	stopGate           playback.DLNAStopGate
}

func NewDLNA(ctx context.Context, cfg DLNAConfig) (*DLNA, error) {
	if ctx == nil {
		return nil, errors.New("DLNA context required")
	}
	payload, err := soapcalls.NewTVPayload(&soapcalls.Options{Ctx: ctx, DMR: cfg.Endpoint, LogOutput: cfg.LogOutput})
	if err != nil {
		return nil, fmt.Errorf("open DLNA transport: %w", err)
	}
	return &DLNA{payload: payload, callbackURL: cfg.CallbackURL, callbacks: cfg.Callbacks}, nil
}

func (d *DLNA) call(ctx context.Context, fn func(*soapcalls.TVPayload) error) error {
	if ctx == nil {
		return errors.New("DLNA context required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.payload.SetContext(ctx)
	return fn(d.payload)
}

func (d *DLNA) Load(ctx context.Context, req playback.LoadRequest) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error {
		if d.callbacks != nil && d.callbacksRequested {
			if _, err := d.callbacks.replaceStream(d.callbackGeneration); err != nil {
				return err
			}
			d.callbackEpoch++
		}
		previousSID := d.sid
		knownSubscriptions := make(map[string]struct{})
		for _, sid := range p.SubscriptionIDs() {
			knownSubscriptions[sid] = struct{}{}
		}
		p.MediaURL, p.MediaType, p.SubtitlesURL = req.MediaURL, req.MediaType, req.SubtitleURL
		p.Seekable = req.Seekable
		p.Metadata = req.Metadata
		d.mediaType = req.MediaType
		d.sid = ""
		d.callbacksActive = false
		d.stopGate.Reset()
		if d.callbackURL != nil && d.callbackURL.CallbackURL() != "" {
			p.CallbackURL = d.callbackURL.CallbackURL()
			if err := p.GetProtocolInfo(); err != nil {
				return err
			}
			if err := p.SubscribeSoapCall(""); err != nil {
				return err
			}
			ids := p.SubscriptionIDs()
			for _, sid := range ids {
				if _, existed := knownSubscriptions[sid]; !existed {
					d.sid = sid
					break
				}
			}
			if d.sid == "" {
				for _, sid := range ids {
					if sid == previousSID {
						d.sid = sid
						break
					}
				}
			}
			if d.sid == "" && len(ids) > 0 {
				d.sid = ids[len(ids)-1]
			}
			if err := d.configureCallbacksLocked(); err != nil {
				return err
			}
		}
		return p.SetAVTransportURI()
	})
}

func (d *DLNA) ActivateCallbacks(generation uint64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.callbacks == nil {
		return nil
	}
	if !d.callbacksRequested || d.callbackGeneration != generation {
		d.callbackGeneration = generation
		d.callbacksRequested = true
		d.callbacksActive = false
		d.callbackEpoch++
		d.callbacks.register(generation)
	}
	if d.callbacksActive {
		return nil
	}
	return d.configureCallbacksLocked()
}

func (d *DLNA) configureCallbacksLocked() error {
	if d.callbacks == nil || !d.callbacksRequested || d.sid == "" {
		return nil
	}
	generation, sid, epoch := d.callbackGeneration, d.sid, d.callbackEpoch
	if err := d.callbacks.configure(generation, sid, net.ParseIP(d.payload.PinnedIP), d.mediaType, func(stream *callbackStream) httphandlers.CallbackGapHandler {
		return callbackGapRecovery{dlna: d, generation: generation, sid: sid, epoch: epoch, stream: stream}
	}); err != nil {
		return err
	}
	d.callbacksActive = true
	return nil
}

// SuppressCallbackStops controls intentional STOPPED callbacks during a reload.
func (d *DLNA) SuppressCallbackStops(generation uint64, suppress bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.callbacks == nil {
		return nil
	}
	if !d.callbacksRequested || d.callbackGeneration != generation {
		return errors.New("callback generation superseded")
	}
	return d.callbacks.SuppressCallbackStops(generation, suppress)
}

type callbackGapRecovery struct {
	dlna       *DLNA
	generation uint64
	sid        string
	epoch      uint64
	stream     *callbackStream
}

func (r callbackGapRecovery) CallbackGap(ctx context.Context, _ string, previous, next uint32) {
	r.dlna.recoverCallbackGap(ctx, r, previous, next)
}

func (d *DLNA) CallbackGap(ctx context.Context, sid string, _, _ uint32) {
	d.mu.Lock()
	if d.callbacks == nil || !d.callbacksActive || sid != d.sid {
		d.mu.Unlock()
		return
	}
	recovery := callbackGapRecovery{
		dlna: d, generation: d.callbackGeneration, sid: sid,
		epoch: d.callbackEpoch, stream: d.callbacks.currentStream(d.callbackGeneration),
	}
	d.mu.Unlock()
	d.recoverCallbackGap(ctx, recovery, 0, 0)
}

func (d *DLNA) recoverCallbackGap(ctx context.Context, recovery callbackGapRecovery, _, _ uint32) {
	if ctx == nil || recovery.stream == nil {
		return
	}
	d.mu.Lock()
	if d.callbacks == nil || !d.callbacksActive ||
		d.callbackGeneration != recovery.generation || d.callbackEpoch != recovery.epoch || d.sid != recovery.sid {
		d.mu.Unlock()
		return
	}
	d.payload.SetContext(ctx)
	_ = d.payload.SubscribeSoapCall(recovery.sid)
	values, err := d.payload.GetTransportInfo()
	if err != nil || len(values) == 0 {
		d.mu.Unlock()
		return
	}
	state := strings.ToUpper(strings.TrimSpace(values[0]))
	d.mu.Unlock()
	if state != "" {
		recovery.stream.deliver(playback.DLNACallbackEvent{Generation: recovery.generation, TransportState: state})
	}
}

func (d *DLNA) Play(ctx context.Context) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error { return p.PlayPauseStopSoapCall("Play") })
}
func (d *DLNA) Pause(ctx context.Context) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error { return p.PlayPauseStopSoapCall("Pause") })
}
func (d *DLNA) Stop(ctx context.Context) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error { return p.SendtoTV("Stop") })
}
func (d *DLNA) Seek(ctx context.Context, value string) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error { return p.SeekSoapCall(value) })
}

func (d *DLNA) SetNext(ctx context.Context, req playback.LoadRequest) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error {
		p.MediaURL, p.MediaType, p.SubtitlesURL = req.MediaURL, req.MediaType, req.SubtitleURL
		p.Seekable = req.Seekable
		p.Metadata = req.Metadata
		return p.SetNextAVTransportURI(false)
	})
}

func (d *DLNA) NextURI(ctx context.Context) (string, error) {
	var nextURI string
	err := d.call(ctx, func(p *soapcalls.TVPayload) error {
		var err error
		nextURI, err = p.Gapless()
		return err
	})
	return nextURI, err
}

func (d *DLNA) ClearNext(ctx context.Context) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error { return p.SetNextAVTransportURI(true) })
}

func (d *DLNA) Position(ctx context.Context) (playback.Position, error) {
	var result playback.Position
	err := d.call(ctx, func(p *soapcalls.TVPayload) error {
		values, err := p.GetPositionInfo()
		if err != nil {
			return err
		}
		if len(values) < 2 {
			return errors.New("DLNA position response incomplete")
		}
		result.Duration, err = parseClock(values[0])
		if err != nil {
			return err
		}
		result.Current, err = parseClock(values[1])
		return err
	})
	return result, err
}

func (d *DLNA) Volume(ctx context.Context) (int, error) {
	var volume int
	err := d.call(ctx, func(p *soapcalls.TVPayload) error {
		var err error
		volume, err = p.GetVolumeSoapCall()
		return err
	})
	return volume, err
}
func (d *DLNA) SetVolume(ctx context.Context, volume int) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error { return p.SetVolumeSoapCall(strconv.Itoa(volume)) })
}
func (d *DLNA) SetMute(ctx context.Context, muted bool) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error {
		if muted {
			return p.SetMuteSoapCall("1")
		}
		return p.SetMuteSoapCall("0")
	})
}

func (d *DLNA) Close(ctx context.Context) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error {
		var first error
		for _, sid := range p.SubscriptionIDs() {
			if err := p.UnsubscribeSoapCall(sid); err != nil && first == nil {
				first = err
			}
		}
		if d.callbacks != nil && d.callbacksRequested {
			d.callbacks.CloseGeneration(d.callbackGeneration)
			d.callbacksActive = false
			d.callbackEpoch++
		}
		return first
	})
}

func parseClock(value string) (int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid renderer time %q", value)
	}
	hours, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	minutes, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, err
	}
	return hours*3600 + minutes*60 + int(seconds), nil
}

// Chromecast preserves CastClient retry/reconnect/logging behavior.
type Chromecast struct {
	mu        sync.Mutex
	client    *castprotocol.CastClient
	interrupt func() error
}

type chromecastStatusResult struct {
	status playback.CastStatus
	err    error
}

func NewChromecast(endpoint string, logOutput io.Writer) (*Chromecast, error) {
	client, err := castprotocol.NewCastClient(endpoint)
	if err != nil {
		return nil, err
	}
	client.LogOutput = logOutput
	return &Chromecast{client: client, interrupt: func() error { return client.Close(false) }}, nil
}

func (c *Chromecast) call(ctx context.Context, fn func() error) error {
	if ctx == nil {
		return errors.New("Chromecast context required")
	}
	result := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		result <- func() error {
			c.mu.Lock()
			defer c.mu.Unlock()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			close(started)
			return fn()
		}()
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
	}

	// A canceled queued call must not interrupt the operation currently holding
	// the mutex. Wait until this call either starts or rejects cancellation.
	select {
	case err := <-result:
		return err
	case <-started:
	}
	select {
	case err := <-result:
		return err
	default:
	}

	interruptDone := make(chan struct{})
	go func() {
		if c.interrupt != nil {
			_ = c.interrupt()
		}
		close(interruptDone)
	}()
	<-result
	<-interruptDone
	return ctx.Err()
}

// statusCall lets a canceled monitor return without closing the shared Cast
// connection. The in-flight status request finishes under the adapter mutex,
// then a replacement load can reuse the same receiver session.
func (c *Chromecast) statusCall(ctx context.Context, fn func() (playback.CastStatus, error)) (playback.CastStatus, error) {
	if ctx == nil {
		return playback.CastStatus{}, errors.New("Chromecast context required")
	}
	result := make(chan chromecastStatusResult, 1)
	go func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if err := ctx.Err(); err != nil {
			result <- chromecastStatusResult{err: err}
			return
		}
		status, err := fn()
		result <- chromecastStatusResult{status: status, err: err}
	}()
	select {
	case got := <-result:
		return got.status, got.err
	case <-ctx.Done():
		return playback.CastStatus{}, ctx.Err()
	}
}

func (c *Chromecast) Connect(ctx context.Context) error { return c.call(ctx, c.client.Connect) }
func (c *Chromecast) Load(ctx context.Context, req playback.LoadRequest) error {
	return c.call(ctx, func() error {
		return c.client.LoadMedia(castprotocol.LoadRequest{MediaURL: req.MediaURL, ContentType: req.MediaType, Metadata: req.Metadata, StartTime: req.Start, Duration: float64(req.Duration), SubtitleURL: req.SubtitleURL})
	})
}
func (c *Chromecast) LoadOnExisting(ctx context.Context, req playback.LoadRequest) error {
	return c.call(ctx, func() error {
		return c.client.LoadMediaOnExisting(castprotocol.LoadRequest{MediaURL: req.MediaURL, ContentType: req.MediaType, Metadata: req.Metadata, StartTime: req.Start, Duration: float64(req.Duration), SubtitleURL: req.SubtitleURL})
	})
}
func (c *Chromecast) Play(ctx context.Context) error  { return c.call(ctx, c.client.Play) }
func (c *Chromecast) Pause(ctx context.Context) error { return c.call(ctx, c.client.Pause) }
func (c *Chromecast) Stop(ctx context.Context) error  { return c.call(ctx, c.client.Stop) }
func (c *Chromecast) Seek(ctx context.Context, seconds int) error {
	return c.call(ctx, func() error { return c.client.Seek(seconds) })
}
func (c *Chromecast) Close(ctx context.Context) error {
	return c.call(ctx, func() error { return c.client.Close(false) })
}
func (c *Chromecast) Volume(ctx context.Context) (int, error) {
	var volume int
	err := c.call(ctx, func() error {
		status, err := c.client.GetStatus()
		if err != nil {
			return err
		}
		volume = max(0, min(100, int(math.Round(float64(status.Volume)*100))))
		return nil
	})
	return volume, err
}
func (c *Chromecast) SetVolume(ctx context.Context, volume int) error {
	return c.call(ctx, func() error { return c.client.SetVolume(float32(volume) / 100) })
}
func (c *Chromecast) SetMute(ctx context.Context, muted bool) error {
	return c.call(ctx, func() error { return c.client.SetMuted(muted) })
}
func (c *Chromecast) Status(ctx context.Context) (playback.CastStatus, error) {
	return c.statusCall(ctx, func() (playback.CastStatus, error) {
		status, err := c.client.GetStatus()
		if err != nil {
			return playback.CastStatus{}, err
		}
		return playback.CastStatus{
			PlayerState: status.PlayerState,
			Current:     int(status.CurrentTime),
			Duration:    int(status.Duration),
			ContentType: status.ContentType,
			MediaTitle:  status.MediaTitle,
		}, nil
	})
}

type Factory struct {
	LogOutput   io.Writer
	CallbackURL CallbackURLProvider
	Callbacks   *CallbackBridge
}

func (f *Factory) Open(ctx context.Context, device playback.Device) (playback.Transport, error) {
	switch device.Protocol {
	case devices.DeviceTypeDLNA:
		return NewDLNA(ctx, DLNAConfig{Endpoint: device.Endpoint, LogOutput: f.LogOutput, CallbackURL: f.CallbackURL, Callbacks: f.Callbacks})
	case devices.DeviceTypeChromecast:
		cast, err := NewChromecast(device.Endpoint, f.LogOutput)
		if err != nil {
			return nil, err
		}
		if err := cast.Connect(ctx); err != nil {
			return nil, err
		}
		return cast, nil
	default:
		return nil, fmt.Errorf("unsupported renderer protocol %q", device.Protocol)
	}
}

func RunMonitor(ctx context.Context, cfg playback.MonitorConfig, device playback.Device, transport playback.Transport) {
	generation, sink := cfg.Generation, cfg.Sink
	if sink == nil {
		return
	}
	switch typed := transport.(type) {
	case *DLNA:
		if err := typed.ActivateCallbacks(generation); err != nil {
			sink.HandleMonitorEvent(ctx, playback.MonitorEvent{Generation: generation, Terminal: playback.TerminalError, Err: err})
			return
		}
		var callbacks <-chan playback.DLNACallbackEvent
		if typed.callbacks != nil {
			callbacks = typed.callbacks.Events(generation)
		}
		cfg.DLNAStopGate = &typed.stopGate
		playback.RunDLNAMonitor(ctx, cfg, typed, callbacks)
	case *Chromecast:
		playback.RunChromecastMonitor(ctx, playback.ChromecastMonitorConfig{MonitorConfig: cfg}, typed)
	default:
		sink.HandleMonitorEvent(ctx, playback.MonitorEvent{Generation: generation, Terminal: playback.TerminalError, Err: fmt.Errorf("monitor unavailable for %s", device.Protocol)})
	}
}

var (
	_ playback.DiscoveryScanner     = Scanner{}
	_ playback.DLNATransport        = (*DLNA)(nil)
	_ playback.DLNAGaplessTransport = (*DLNA)(nil)
	_ playback.Transport            = (*DLNA)(nil)
	_ playback.ChromecastTransport  = (*Chromecast)(nil)
	_ playback.Transport            = (*Chromecast)(nil)
)
