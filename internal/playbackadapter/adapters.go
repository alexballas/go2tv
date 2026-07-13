package playbackadapter

import (
	"bytes"
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

	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/devices"
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/metadata"
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

// CallbackBridge is a stable HTTP callback target with replaceable validated sessions.
type CallbackBridge struct {
	mu      sync.RWMutex
	session *httphandlers.CallbackSession
	events  chan playback.DLNACallbackEvent
}

func NewCallbackBridge() *CallbackBridge {
	return &CallbackBridge{events: make(chan playback.DLNACallbackEvent, callbackQueueSize)}
}

func (b *CallbackBridge) Events() <-chan playback.DLNACallbackEvent { return b.events }

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
	session, err := httphandlers.NewCallbackSession(httphandlers.CallbackSessionConfig{
		Generation: generation, SID: sid, SourceIP: source, MediaType: mediaType,
		QueueSize: callbackQueueSize,
		Sink: httphandlers.CallbackEventSinkFunc(func(_ context.Context, event httphandlers.CallbackEvent) {
			select {
			case b.events <- playback.DLNACallbackEvent{Generation: event.Generation, TransportState: event.TransportState}:
			default:
			}
		}),
		GapHandler: gap,
	})
	if err != nil {
		return err
	}
	b.mu.Lock()
	old := b.session
	b.session = session
	b.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return nil
}

func (b *CallbackBridge) Close() {
	b.mu.Lock()
	session := b.session
	b.session = nil
	b.mu.Unlock()
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
	mu          sync.Mutex
	payload     *soapcalls.TVPayload
	callbackURL CallbackURLProvider
	callbacks   *CallbackBridge
	sid         string
	mediaType   string
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
		p.MediaURL, p.MediaType, p.SubtitlesURL = req.MediaURL, req.MediaType, req.SubtitleURL
		p.Seekable = req.Seekable
		p.Metadata = req.Metadata
		d.mediaType = req.MediaType
		if d.callbackURL != nil && d.callbackURL.CallbackURL() != "" {
			p.CallbackURL = d.callbackURL.CallbackURL()
			if err := p.GetProtocolInfo(); err != nil {
				return err
			}
			if err := p.SubscribeSoapCall(""); err != nil {
				return err
			}
			ids := p.SubscriptionIDs()
			if len(ids) > 0 {
				d.sid = ids[0]
			}
		}
		return p.SetAVTransportURI()
	})
}

func (d *DLNA) ActivateCallbacks(generation uint64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.callbacks == nil || d.sid == "" {
		return nil
	}
	return d.callbacks.Configure(generation, d.sid, net.ParseIP(d.payload.PinnedIP), d.mediaType, d)
}

func (d *DLNA) CallbackGap(ctx context.Context, sid string, _, _ uint32) {
	_ = d.Resubscribe(ctx, sid)
	_, _ = d.State(ctx)
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

func (d *DLNA) State(ctx context.Context) (string, error) {
	var state string
	err := d.call(ctx, func(p *soapcalls.TVPayload) error {
		values, err := p.GetTransportInfo()
		if err != nil {
			return err
		}
		if len(values) == 0 {
			return errors.New("DLNA state response incomplete")
		}
		state = values[0]
		return nil
	})
	return state, err
}

func (d *DLNA) SetNextURI(ctx context.Context, uri, _ string) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error { p.MediaURL = uri; return p.SetNextAVTransportURI(false) })
}
func (d *DLNA) ClearNextURI(ctx context.Context) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error { return p.SetNextAVTransportURI(true) })
}
func (d *DLNA) Resubscribe(ctx context.Context, sid string) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error { return p.SubscribeSoapCall(sid) })
}
func (d *DLNA) Unsubscribe(ctx context.Context, sid string) error {
	return d.call(ctx, func(p *soapcalls.TVPayload) error { return p.UnsubscribeSoapCall(sid) })
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
		if d.callbacks != nil {
			d.callbacks.Close()
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
	mu     sync.Mutex
	client *castprotocol.CastClient
}

func NewChromecast(endpoint string, logOutput io.Writer) (*Chromecast, error) {
	client, err := castprotocol.NewCastClient(endpoint)
	if err != nil {
		return nil, err
	}
	client.LogOutput = logOutput
	return &Chromecast{client: client}, nil
}

func (c *Chromecast) call(ctx context.Context, fn func() error) error {
	if ctx == nil {
		return errors.New("Chromecast context required")
	}
	result := make(chan error, 1)
	go func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if ctx.Err() != nil {
			result <- ctx.Err()
			return
		}
		result <- fn()
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-result:
		return err
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
	var result playback.CastStatus
	err := c.call(ctx, func() error {
		status, err := c.client.GetStatus()
		if err != nil {
			return err
		}
		result = playback.CastStatus{PlayerState: status.PlayerState, Current: int(status.CurrentTime), Duration: int(status.Duration)}
		return nil
	})
	return result, err
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

func RunMonitor(ctx context.Context, generation uint64, device playback.Device, transport playback.Transport, sink playback.MonitorSink) {
	switch typed := transport.(type) {
	case *DLNA:
		if err := typed.ActivateCallbacks(generation); err != nil {
			sink.HandleMonitorEvent(ctx, playback.MonitorEvent{Generation: generation, Terminal: playback.TerminalError, Err: err})
			return
		}
		var callbacks <-chan playback.DLNACallbackEvent
		if typed.callbacks != nil {
			callbacks = typed.callbacks.Events()
		}
		playback.RunDLNAMonitor(ctx, playback.MonitorConfig{Generation: generation, Sink: sink}, typed, callbacks)
	case *Chromecast:
		playback.RunChromecastMonitor(ctx, playback.ChromecastMonitorConfig{MonitorConfig: playback.MonitorConfig{Generation: generation, Sink: sink}}, typed)
	default:
		sink.HandleMonitorEvent(ctx, playback.MonitorEvent{Generation: generation, Terminal: playback.TerminalError, Err: fmt.Errorf("monitor unavailable for %s", device.Protocol)})
	}
}

// Artwork resolves normalized legacy artwork without Fyne dependencies.
type Artwork struct{}

func (Artwork) Resolve(ctx context.Context, source string) (io.ReadCloser, string, error) {
	if ctx == nil {
		return nil, "", errors.New("artwork context required")
	}
	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	default:
	}
	asset, err := metadata.ResolveArtwork(source)
	if err != nil {
		return nil, "", err
	}
	return io.NopCloser(bytes.NewReader(asset.Data)), asset.MIMEType, nil
}

var (
	_ playback.DiscoveryScanner    = Scanner{}
	_ playback.DLNATransport       = (*DLNA)(nil)
	_ playback.Transport           = (*DLNA)(nil)
	_ playback.ChromecastTransport = (*Chromecast)(nil)
	_ playback.Transport           = (*Chromecast)(nil)
	_ playback.Artwork             = Artwork{}
)
