package playback

import (
	"context"
	"io"
	"time"

	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/metadata"
)

// Device is a renderer safe to expose outside discovery. Endpoint is transport-only.
type Device struct {
	ID        string
	Name      string
	Protocol  string
	AudioOnly bool
	Endpoint  string
}

type Discovery interface {
	Start(context.Context)
	Refresh(context.Context) error
	Snapshot() []Device
	Subscribe(int) (<-chan []Device, func())
}

type DiscoveryScanner interface {
	Scan(context.Context) ([]Device, error)
}

type DLNAPositionTransport interface {
	Position(context.Context) (Position, error)
}

type DLNATransport interface {
	Load(context.Context, LoadRequest) error
	Play(context.Context) error
	Stop(context.Context) error
	Seek(context.Context, string) error
	Position(context.Context) (Position, error)
}

// DLNAGaplessTransport is the optional DLNA SetNextAVTransportURI surface.
// NextURI reports the renderer's currently staged URI; an empty URI after a
// successful SetNext marks promotion to that item on compatible renderers.
type DLNAGaplessTransport interface {
	SetNext(context.Context, LoadRequest) error
	NextURI(context.Context) (string, error)
	ClearNext(context.Context) error
}

// DLNAMonitorGuard serializes a poll and its handoff with queue publication.
type DLNAMonitorGuard interface {
	TryMonitorLock() (release func(), acquired bool)
}

// DLNAStartupRetryTransport re-sends a start the renderer acknowledged but
// never began. reload also re-sends SetAVTransportURI for uri first.
type DLNAStartupRetryTransport interface {
	RetryStart(ctx context.Context, uri string, reload bool) error
}

// DLNAStatusTransport provides authoritative state when callbacks are delayed.
type DLNAStatusTransport interface {
	TransportState(context.Context) (string, error)
}

// DLNAMediaInfoTransport retains both URIs from a single GetMediaInfo request.
type DLNAMediaInfoTransport interface {
	MediaInfo(context.Context) (currentURI, nextURI string, err error)
}

type ChromecastTransport interface {
	LoadOnExisting(context.Context, LoadRequest) error
	Seek(context.Context, int) error
	Status(context.Context) (CastStatus, error)
}

// Transport is the controller-facing common renderer surface.
type Transport interface {
	Load(context.Context, LoadRequest) error
	Play(context.Context) error
	Pause(context.Context) error
	Stop(context.Context) error
	Close(context.Context) error
	Volume(context.Context) (int, error)
	SetVolume(context.Context, int) error
	SetMute(context.Context, bool) error
}

type MediaServer interface {
	Start(context.Context, ServerRequest) (MediaRoute, error)
	Stop(context.Context) error
	Add(context.Context, RouteRequest) (MediaRoute, error)
	Remove(context.Context, string) error
}

// SourceOpener returns a fresh, already-confined handle. Callers must never
// derive filesystem access from renderer routes or display names.
type SourceOpener func(context.Context) (io.ReadSeekCloser, time.Time, error)

type Clock interface {
	NewTicker(time.Duration) Ticker
	NewTimer(time.Duration) Timer
}

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type Random interface {
	Read([]byte) (int, error)
}

type Position struct {
	TrackURI string
	Current  int
	Duration int
}

type CastStatus struct {
	PlayerState string
	Current     int
	Duration    int
	ContentType string
	MediaTitle  string
}

type LoadRequest struct {
	MediaURL           string
	MediaType          string
	SubtitleURL        string
	TorrentSubtitleURL string
	Start              int
	Duration           float64
	Seekable           bool
	Transcode          bool
	// Live marks an endless stream; Chromecast shows it as LIVE.
	Live        bool
	Metadata    metadata.Media
	ArtworkData []byte
}

type ServerRequest struct {
	Media         SourceOpener
	MediaExt      string
	MediaType     string
	Subtitle      SourceOpener
	SubtitleExt   string
	Transcode     bool
	SeekOffset    int
	Duration      float64
	BurnSubtitle  bool
	Target        Device
	TorrentSource mediasource.Source
	// Live relays an endless network stream in place of Media.
	Live *LiveSource
}

// LiveSource is an endless http(s) stream such as internet radio. The media
// server opens it once per renderer request; OnTitle receives ICY track titles
// parsed from that connection.
type LiveSource struct {
	URL     string
	OnTitle func(string)
}

type RouteRequest struct {
	ID        string
	Open      SourceOpener
	Extension string
	MediaType string
	Contents  []byte
}

type MediaRoute struct {
	URL                string
	SubtitleURL        string
	ID                 string
	SubtitleID         string
	TorrentSubtitleURL string
	TorrentSubtitleID  string
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }
func (realClock) NewTimer(d time.Duration) Timer   { return realTimer{time.NewTimer(d)} }

type realTicker struct{ *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.Ticker.C }

type realTimer struct{ *time.Timer }

func (t realTimer) C() <-chan time.Time { return t.Timer.C }
