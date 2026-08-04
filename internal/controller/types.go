package controller

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/metadata"
)

const (
	// MaxQueueItems is the maximum number of queued media items.
	MaxQueueItems = 1000
	// DefaultImageTime is the default image display duration in seconds.
	DefaultImageTime = 10
	// MinImageTime is the shortest non-zero image display duration in seconds.
	MinImageTime = 5
	// MaxImageTime is the longest image display duration in seconds.
	MaxImageTime = 300
	// ArtworkCacheBytes is the default artwork cache byte limit.
	ArtworkCacheBytes = 64 << 20

	// Playback state values are stable, machine-readable snapshot values. New
	// adapter states may be added; callers must tolerate unknown values.
	PlaybackStateStopped   = "STOPPED"
	PlaybackStateLoading   = "LOADING"
	PlaybackStatePlaying   = "PLAYING"
	PlaybackStatePaused    = "PAUSED"
	PlaybackStateBuffering = "BUFFERING"
	PlaybackStateIdle      = "IDLE"
	PlaybackStateStopping  = "STOPPING"
)

const defaultOperationTimeout = 30 * time.Second

// Sentinel errors support errors.Is. Command callers normally consume the
// corresponding Result.Code; adapter and lifecycle callers may use the errors.
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
	ErrInvalidConfig    = errors.New("invalid controller config")
	ErrInvalidMedia     = errors.New("invalid media reference")
	ErrInvalidSubtitle  = errors.New("invalid subtitle reference")
	ErrInvalidArtwork   = errors.New("invalid artwork")
	ErrSeekUnsupported  = errors.New("seek unsupported")
)

// ErrorCode is a stable machine-readable failure category. New codes may be
// added; callers must tolerate unknown values.
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
	CodeCanceled   ErrorCode = "canceled"
	CodeDeadline   ErrorCode = "deadline_exceeded"
)

// Result is the outcome of a controller command. An empty Code means success.
// RequestID is copied verbatim from the request and Revision is the controller
// revision associated with the outcome.
type Result struct {
	RequestID string    `json:"RequestID"`
	Revision  uint64    `json:"Revision"`
	Code      ErrorCode `json:"Code"`
	Message   string    `json:"Message"`
}

// OK reports whether the command succeeded.
func (r Result) OK() bool { return r.Code == "" }

// Err converts a failed result to an error that preserves its stable code.
func (r Result) Err() error {
	if r.OK() {
		return nil
	}
	return &OperationError{Code: r.Code, Message: r.Message}
}

// OperationError is the error form of a failed Result. Its exported fields are
// safe to serialize. errors.Is recognizes the corresponding controller or
// context sentinel where one exists.
type OperationError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (e *OperationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *OperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return errorForCode(e.Code)
}

// ErrorCodeOf classifies controller, context, and known playback errors. It
// returns CodeInternal for unknown non-nil errors and the empty code for nil.
func ErrorCodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var operationError *OperationError
	if errors.As(err, &operationError) && operationError != nil && operationError.Code != "" {
		return operationError.Code
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return CodeDeadline
	case errors.Is(err, context.Canceled):
		return CodeCanceled
	case errors.Is(err, ErrBusy):
		return CodeBusy
	case errors.Is(err, ErrConflict):
		return CodeConflict
	case errors.Is(err, ErrNotFound):
		return CodeNotFound
	case errors.Is(err, ErrNoDevice):
		return CodeNoDevice
	case errors.Is(err, ErrNoMedia):
		return CodeNoMedia
	case errors.Is(err, ErrNoSession):
		return CodeNoSession
	case errors.Is(err, ErrAudioOnly):
		return CodeAudioOnly
	case errors.Is(err, ErrQueueLimit):
		return CodeQueueLimit
	case errors.Is(err, ErrInvalidPolicy), errors.Is(err, ErrInvalidOperation),
		errors.Is(err, ErrInvalidConfig), errors.Is(err, ErrInvalidMedia),
		errors.Is(err, ErrInvalidSubtitle), errors.Is(err, ErrInvalidArtwork),
		errors.Is(err, ErrSeekUnsupported),
		errors.Is(err, playback.ErrSeekUnsupported), errors.Is(err, playback.ErrSeekNegative),
		errors.Is(err, playback.ErrSeekPastDuration):
		return CodeInvalid
	case errors.Is(err, ErrClosed):
		return CodeClosed
	default:
		return CodeInternal
	}
}

func errorForCode(code ErrorCode) error {
	switch code {
	case CodeBusy:
		return ErrBusy
	case CodeConflict:
		return ErrConflict
	case CodeNotFound:
		return ErrNotFound
	case CodeNoDevice:
		return ErrNoDevice
	case CodeNoMedia:
		return ErrNoMedia
	case CodeNoSession:
		return ErrNoSession
	case CodeAudioOnly:
		return ErrAudioOnly
	case CodeQueueLimit:
		return ErrQueueLimit
	case CodeInvalid:
		return ErrInvalidOperation
	case CodeClosed:
		return ErrClosed
	case CodeCanceled:
		return context.Canceled
	case CodeDeadline:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// Policy controls automatic queue traversal. The zero value is valid and
// disables all automatic behavior. A zero ImageDurationSeconds disables timed
// image advance; DefaultPolicy supplies the recommended duration.
type Policy struct {
	LoopSelected         bool `json:"LoopSelected"`
	AutoPlayNext         bool `json:"AutoPlayNext"`
	AutoPlaySameType     bool `json:"AutoPlaySameType"`
	GaplessEnabled       bool `json:"GaplessEnabled"`
	ImageDurationSeconds int  `json:"ImageDurationSeconds"`
}

// DefaultPolicy returns the policy used by a new Controller.
func DefaultPolicy() Policy { return Policy{ImageDurationSeconds: DefaultImageTime} }

func (p Policy) Validate() error {
	if p.ImageDurationSeconds != 0 && (p.ImageDurationSeconds < MinImageTime || p.ImageDurationSeconds > MaxImageTime) {
		return fmt.Errorf("image duration: %w", ErrInvalidPolicy)
	}
	if p.LoopSelected && p.AutoPlayNext {
		return fmt.Errorf("loop and autoplay are mutually exclusive: %w", ErrInvalidPolicy)
	}
	if !p.AutoPlayNext && (p.AutoPlaySameType || p.GaplessEnabled) {
		return fmt.Errorf("autoplay options require autoplay: %w", ErrInvalidPolicy)
	}
	return nil
}

// QueueItem is a detached, serialization-safe queue projection. IDs are
// opaque and stable only for the lifetime of the item in this Controller.
type QueueItem struct {
	ID         string               `json:"ID"`
	Name       string               `json:"Name"`
	Parent     string               `json:"Parent"`
	MediaKind  mediamodel.MediaKind `json:"MediaKind"`
	IsSelected bool                 `json:"IsSelected"`
	IsActive   bool                 `json:"IsActive"`
}

// Snapshot is an immutable point-in-time controller projection. Slice values
// do not alias controller-owned storage. Revision changes whenever observable
// state commits; Generation identifies a playback session attempt.
type Snapshot struct {
	Revision         uint64                  `json:"Revision"`
	Devices          []playback.Device       `json:"Devices"`
	SelectedDeviceID string                  `json:"SelectedDeviceID"`
	ActiveDeviceID   string                  `json:"ActiveDeviceID"`
	SelectedMedia    string                  `json:"SelectedMedia"`
	SelectedSubtitle string                  `json:"SelectedSubtitle"`
	ActiveMediaName  string                  `json:"ActiveMediaName"`
	Queue            []QueueItem             `json:"Queue"`
	Transcode        bool                    `json:"Transcode"`
	Generation       uint64                  `json:"Generation"`
	HasSession       bool                    `json:"HasSession"`
	PlaybackState    string                  `json:"PlaybackState"`
	Position         int                     `json:"Position"`
	Duration         int                     `json:"Duration"`
	Volume           int                     `json:"Volume"`
	Muted            bool                    `json:"Muted"`
	MediaType        mediamodel.MediaKind    `json:"MediaType"`
	ArtworkID        string                  `json:"ArtworkID"`
	Policy           Policy                  `json:"Policy"`
	LastError        string                  `json:"LastError"`
	TerminalReason   playback.TerminalReason `json:"TerminalReason"`
}

// Mutation carries optional request correlation and optimistic concurrency.
// ExpectedRevision nil means no precondition; a non-nil stale value fails with
// CodeConflict without applying the mutation.
type Mutation struct {
	RequestID        string  `json:"RequestID"`
	ExpectedRevision *uint64 `json:"ExpectedRevision"`
}

// PlayRequest starts the selected media or QueueItemID. A blank QueueItemID
// uses the current selection.
type PlayRequest struct {
	Mutation
	QueueItemID string `json:"QueueItemID"`
	ctx         context.Context
	target      *playback.Device
	media       *MediaRef
	queueMedia  bool
}

// MediaArtworkLoader lazily resolves normalized artwork for one media item.
type MediaArtworkLoader func(context.Context) (*metadata.ArtworkAsset, error)

// MediaRef is an in-process media capability, not a wire DTO. Each opener must
// return a fresh handle; the consumer closes it. LoadArtwork is attempted only
// when the controller prepares a renderer load.
type MediaRef struct {
	RootID        string
	ID            string
	AbsolutePath  string
	Name          string
	Parent        string
	Kind          mediamodel.MediaKind
	MIMEType      string
	OpenDirect    playback.SourceOpener
	OpenTranscode playback.SourceOpener
	LoadArtwork   MediaArtworkLoader

	artwork          *metadata.ArtworkAsset
	artworkAttempted bool
}

// Validate checks that the reference can be selected and served.
func (r MediaRef) Validate() error {
	switch {
	case strings.TrimSpace(r.RootID) == "":
		return fmt.Errorf("root ID: %w", ErrInvalidMedia)
	case strings.TrimSpace(r.ID) == "":
		return fmt.Errorf("media ID: %w", ErrInvalidMedia)
	case strings.TrimSpace(r.Name) == "":
		return fmt.Errorf("name: %w", ErrInvalidMedia)
	case r.Kind == mediamodel.MediaKindUnknown:
		return fmt.Errorf("kind: %w", ErrInvalidMedia)
	case r.OpenDirect == nil:
		return fmt.Errorf("direct opener: %w", ErrInvalidMedia)
	}
	if value := strings.TrimSpace(r.MIMEType); value != "" {
		if _, _, err := mime.ParseMediaType(value); err != nil {
			return fmt.Errorf("MIME type: %w", ErrInvalidMedia)
		}
	}
	return nil
}

func (r MediaRef) valid() bool { return r.Validate() == nil }

func (r MediaRef) extension() string { return filepath.Ext(r.Name) }

// SubtitleRef is an in-process subtitle capability. The zero value clears the
// current subtitle selection. Open must return a fresh caller-owned handle.
type SubtitleRef struct {
	RootID string
	ID     string
	Name   string
	Open   playback.SourceOpener
}

func (r SubtitleRef) extension() string { return filepath.Ext(r.Name) }

// Validate accepts the zero value and otherwise requires an SRT or VTT source.
func (r SubtitleRef) Validate() error {
	if r.RootID == "" && r.ID == "" && r.Name == "" && r.Open == nil {
		return nil
	}
	switch {
	case strings.TrimSpace(r.RootID) == "":
		return fmt.Errorf("root ID: %w", ErrInvalidSubtitle)
	case strings.TrimSpace(r.ID) == "":
		return fmt.Errorf("subtitle ID: %w", ErrInvalidSubtitle)
	case strings.TrimSpace(r.Name) == "":
		return fmt.Errorf("name: %w", ErrInvalidSubtitle)
	case r.Open == nil:
		return fmt.Errorf("opener: %w", ErrInvalidSubtitle)
	case !mediamodel.IsSRTPath(r.Name) && !mediamodel.IsVTTPath(r.Name):
		return fmt.Errorf("format: %w", ErrInvalidSubtitle)
	default:
		return nil
	}
}

func (r SubtitleRef) valid() bool { return r.Validate() == nil && r.ID != "" }

// PolicyRequest atomically replaces the current policy.
type PolicyRequest struct {
	Mutation
	Policy Policy
}

// QueueAddRequest adds Media if its path is not already queued and optionally
// selects the resulting item.
type QueueAddRequest struct {
	Mutation
	Media  MediaRef
	Select bool
}

// QueueAddResult includes the opaque ID of the added or existing item.
type QueueAddResult struct {
	Result
	ItemID string
}

// QueueAddManyRequest appends Items in order, skipping already-queued paths
// and dropping the remainder once the queue reaches MaxQueueItems.
type QueueAddManyRequest struct {
	Mutation
	Items []MediaRef
}

// QueueAddManyResult counts appended items, duplicates skipped, and items
// dropped for capacity. Counts are zero unless Result is OK or CodeQueueLimit.
type QueueAddManyResult struct {
	Result
	Added      int
	Duplicates int
	Dropped    int
}

// SeekRequest seeks to an absolute non-negative position in seconds.
type SeekRequest struct {
	Mutation
	Seconds int
}

// Transport is the renderer surface owned by Controller after Open succeeds.
type Transport = playback.Transport

// TransportFactory opens one controller-owned transport. Open must honor its
// context and must not return a nil transport with a nil error. On success
// Controller calls Close exactly once, including failed load and shutdown paths.
type TransportFactory interface {
	Open(context.Context, playback.Device) (playback.Transport, error)
}

// Discovery is the minimal device source used by Controller. Controller calls
// Subscribe before Start and calls Start once. Methods must honor their
// contexts. Subscribe must return detached device slices; its unsubscribe
// function may be nil.
type Discovery interface {
	Start(context.Context)
	Refresh(context.Context) error
	Snapshot() []playback.Device
	Subscribe(int) (<-chan []playback.Device, func())
}

// EventLogger receives human-readable lifecycle events. Messages are
// observational, not a machine-readable compatibility contract. Implementations
// must be concurrency-safe, non-blocking, and must not call back into Controller.
type EventLogger interface {
	Debug(string)
	Info(string)
	Warning(string)
	Error(string)
}

// Config supplies optional, caller-owned adapters. Controller does not close
// Discovery, MediaServer, Artwork, Clock, or Logger; it cancels work, closes
// every opened Transport, and stops active media-server routes. A nil adapter
// disables its associated capability. Zero durations receive safe defaults.
type Config struct {
	// ParentContext cancellation shuts down the Controller. Nil uses Background.
	ParentContext context.Context
	// Discovery is optional and remains caller-owned.
	Discovery Discovery
	// TransportFactory and MediaServer are required for playback.
	TransportFactory TransportFactory
	MediaServer      playback.MediaServer
	// Artwork is optional. Nil creates a default cache.
	Artwork *ArtworkCache
	// RunMonitor must honor its context and return when the session ends.
	RunMonitor func(context.Context, playback.MonitorConfig, playback.Device, Transport)
	// DurationProbe is advisory; probe errors do not fail playback.
	DurationProbe func(context.Context, playback.SourceOpener) (float64, error)
	// Clock is optional and intended for deterministic monitor/timer adapters.
	Clock playback.Clock
	// OperationTimeout bounds adapter I/O. Non-positive values default to 30s.
	OperationTimeout time.Duration
	// Logger is optional and remains caller-owned.
	Logger EventLogger
}

// Validate checks configuration combinations without applying defaults. New is
// backward compatible and still accepts partial configurations; operations for
// missing capabilities return CodeInvalid.
func (c Config) Validate() error {
	if c.OperationTimeout < 0 {
		return fmt.Errorf("operation timeout: %w", ErrInvalidConfig)
	}
	if c.MediaServer != nil && c.TransportFactory == nil {
		return fmt.Errorf("media server requires a transport factory: %w", ErrInvalidConfig)
	}
	return nil
}
