package controller

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/metadata"
)

const (
	ActorQueueSize    = 64
	CallbackQueueSize = 128
	MaxQueueItems     = 1000
	DefaultImageTime  = 10
	ArtworkCacheBytes = 64 << 20
)

var (
	ErrClosed           = errors.New("controller closed")
	ErrBusy             = errors.New("operation busy")
	ErrConflict         = errors.New("revision conflict")
	ErrNotFound         = errors.New("not found")
	ErrNoDevice         = errors.New("no selected device")
	ErrNoMedia          = errors.New("no selected media")
	ErrNoSession        = errors.New("no active session")
	ErrAudioOnly        = errors.New("media unsupported by audio-only device")
	ErrQueueLimit       = errors.New("queue limit exceeded")
	ErrInvalidPolicy    = errors.New("invalid playback policy")
	ErrInvalidOperation = errors.New("invalid operation")
	ErrSeekUnsupported  = errors.New("seek unsupported")
)

type ErrorCode string

const (
	CodeBusy       ErrorCode = "busy"
	CodeConflict   ErrorCode = "conflict"
	CodeNotFound   ErrorCode = "not_found"
	CodeNoDevice   ErrorCode = "no_device"
	CodeNoMedia    ErrorCode = "no_media"
	CodeNoSession  ErrorCode = "no_session"
	CodeAudioOnly  ErrorCode = "audio_only"
	CodeQueueLimit ErrorCode = "queue_limit"
	CodeInvalid    ErrorCode = "invalid"
	CodeInternal   ErrorCode = "internal"
	CodeClosed     ErrorCode = "closed"
)

type Result struct {
	RequestID string
	Revision  uint64
	Code      ErrorCode
	Message   string
}

func (r Result) OK() bool { return r.Code == "" }

type Policy struct {
	LoopSelected         bool
	AutoPlayNext         bool
	AutoPlaySameType     bool
	GaplessEnabled       bool
	ImageDurationSeconds int
}

func DefaultPolicy() Policy { return Policy{ImageDurationSeconds: DefaultImageTime} }

type QueueItem struct {
	ID         string
	Name       string
	Parent     string
	MediaKind  mediamodel.MediaKind
	IsSelected bool
	IsActive   bool
}

type Snapshot struct {
	Revision         uint64
	Devices          []playback.Device
	SelectedDeviceID string
	ActiveDeviceID   string
	SelectedMedia    string
	SelectedSubtitle string
	ActiveMediaName  string
	Queue            []QueueItem
	Transcode        bool
	Generation       uint64
	HasSession       bool
	PlaybackState    string
	Position         int
	Duration         int
	Volume           int
	Muted            bool
	MediaType        mediamodel.MediaKind
	ArtworkID        string
	Policy           Policy
	LastError        string
	TerminalReason   playback.TerminalReason
}

type Mutation struct {
	RequestID        string
	ExpectedRevision *uint64
}

type PlayRequest struct {
	Mutation
	QueueItemID string
	target      *playback.Device
	media       *MediaRef
	queueMedia  bool
}

type MediaRef struct {
	RootID        string
	ID            string
	Name          string
	Parent        string
	Kind          mediamodel.MediaKind
	OpenDirect    playback.SourceOpener
	OpenTranscode playback.SourceOpener
	Artwork       *metadata.ArtworkAsset
}

func (r MediaRef) valid() bool {
	return strings.TrimSpace(r.RootID) != "" && strings.TrimSpace(r.ID) != "" && strings.TrimSpace(r.Name) != "" && r.Kind != mediamodel.MediaKindUnknown && r.OpenDirect != nil
}

func (r MediaRef) extension() string { return filepath.Ext(r.Name) }

type SubtitleRef struct {
	RootID string
	ID     string
	Name   string
	Open   playback.SourceOpener
}

func (r SubtitleRef) extension() string { return filepath.Ext(r.Name) }

func (r SubtitleRef) valid() bool {
	return strings.TrimSpace(r.RootID) != "" && strings.TrimSpace(r.ID) != "" && r.Open != nil && (mediamodel.IsSRTPath(r.Name) || mediamodel.IsVTTPath(r.Name))
}

type PolicyRequest struct {
	Mutation
	Policy Policy
}

type QueueAddRequest struct {
	Mutation
	Media  MediaRef
	Select bool
}

type QueueAddResult struct {
	Result
	ItemID string
}

type SeekRequest struct {
	Mutation
	Seconds int
}

type Transport = playback.Transport

type TransportFactory interface {
	Open(context.Context, playback.Device) (playback.Transport, error)
}

type Config struct {
	Discovery        playback.Discovery
	TransportFactory TransportFactory
	MediaServer      playback.MediaServer
	Artwork          *ArtworkCache
	RunMonitor       func(context.Context, uint64, playback.Device, Transport, playback.MonitorSink)
	OperationTimeout time.Duration
}
