package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/metadata"
)

const (
	actorQueueSize    = 64
	callbackQueueSize = 128
)

type message struct {
	fn   func(*actorState)
	done chan struct{}
	call *actorCall
}

type actorCall struct{ state atomic.Uint32 }

const (
	actorCallPending uint32 = iota
	actorCallRunning
	actorCallCanceled
)

type activeSession struct {
	generation       uint64
	target           playback.Device
	itemID           string
	media            MediaRef
	subtitle         SubtitleRef
	kind             mediamodel.MediaKind
	transport        Transport
	server           playback.ServerRequest
	load             playback.LoadRequest
	routeIDs         []string
	ctx              context.Context
	cancel           context.CancelCauseFunc
	reusable         bool
	imageReady       bool
	imageTimer       context.CancelFunc
	imageEpoch       uint64
	seekOffset       int
	expectedDuration int
}

const (
	playOperationPending uint32 = iota
	playOperationAccepted
	playOperationCleanup
)

type playOperation struct {
	generation uint64
	cancel     context.CancelCauseFunc
	done       chan struct{}
	state      atomic.Uint32
	finishOnce sync.Once
}

func (o *playOperation) claim(state uint32) bool {
	return o != nil && o.state.CompareAndSwap(playOperationPending, state)
}

func (o *playOperation) finish() {
	if o != nil {
		o.finishOnce.Do(func() { close(o.done) })
	}
}

func terminalCause(reason playback.TerminalReason) error { return errors.New(string(reason)) }

var errAdapterContract = errors.New("adapter contract violation")

func playbackTime(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	hours := seconds / 3600
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

type actorState struct {
	controller   *Controller
	revision     uint64
	devices      []playback.Device
	selectedID   string
	media        MediaRef
	subtitle     SubtitleRef
	mediaQueueID string
	queue        *mediamodel.Queue
	queueRefs    map[string]MediaRef
	transcode    bool
	policy       Policy
	active       *activeSession
	pending      *playOperation
	generation   uint64
	mutation     bool
	refreshing   bool
	state        string
	position     int
	duration     int
	volume       int
	muted        bool
	artworkID    string
	lastError    string
	terminal     playback.TerminalReason
	deferred     *playback.MonitorEvent
	cleanup      bool
}

type Controller struct {
	cfg       Config
	ctx       context.Context
	cancel    context.CancelCauseFunc
	queue     chan message
	callbacks chan playback.MonitorEvent
	done      chan struct{}
	stopped   chan struct{}
	closeOnce sync.Once
	lifecycle sync.WaitGroup
}

// New creates and starts a Controller. Config is copied and safe defaults are
// applied. Use Config.Validate before New when strict startup validation is
// required. The returned Controller owns all goroutines and opened transports.
func New(cfg Config) *Controller {
	if cfg.OperationTimeout <= 0 {
		cfg.OperationTimeout = defaultOperationTimeout
	}
	if cfg.Artwork == nil {
		cfg.Artwork = NewArtworkCache(ArtworkCacheBytes)
	}
	parent := cfg.ParentContext
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancelCause(parent)
	c := &Controller{cfg: cfg, ctx: ctx, cancel: cancel, queue: make(chan message, actorQueueSize), callbacks: make(chan playback.MonitorEvent, callbackQueueSize), done: make(chan struct{}), stopped: make(chan struct{})}
	go c.run()
	c.goOwned(c.forwardCallbacks)
	if cfg.Discovery != nil {
		updates, unsubscribe := cfg.Discovery.Subscribe(1)
		if unsubscribe == nil {
			unsubscribe = func() {}
		}
		cfg.Discovery.Start(ctx)
		c.goOwned(func() {
			defer unsubscribe()
			for {
				select {
				case <-ctx.Done():
					return
				case devices, ok := <-updates:
					if !ok {
						return
					}
					_ = c.enqueueInternal(message{fn: func(s *actorState) { s.setDevices(devices) }})
				}
			}
		})
	}
	go func() {
		<-c.done
		c.lifecycle.Wait()
		close(c.stopped)
	}()
	return c
}

func (c *Controller) goOwned(fn func()) {
	c.lifecycle.Go(fn)
}

func (c *Controller) run() {
	defer close(c.done)
	s := &actorState{controller: c, policy: DefaultPolicy(), state: PlaybackStateStopped, volume: 100}
	for {
		select {
		case <-c.ctx.Done():
			if s.pending != nil {
				s.pending.cancel(terminalCause(playback.TerminalShutdown))
			} else if s.active != nil {
				s.active.cancel(terminalCause(playback.TerminalShutdown))
				ctx, cancel := context.WithTimeout(context.Background(), c.cfg.OperationTimeout)
				_ = teardownSession(ctx, s.active, c.cfg.MediaServer, c.cfg.OperationTimeout)
				cancel()
			}
			return
		case msg := <-c.queue:
			if msg.call == nil || msg.call.state.CompareAndSwap(actorCallPending, actorCallRunning) {
				msg.fn(s)
			}
			if msg.done != nil {
				close(msg.done)
			}
		}
	}
}

func (c *Controller) forwardCallbacks() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case event := <-c.callbacks:
			select {
			case <-c.ctx.Done():
				return
			case c.queue <- message{fn: func(s *actorState) { s.monitor(event) }}:
			}
		}
	}
}

// Done is closed after shutdown completes and every controller-owned goroutine
// exits. It is safe to call before or after Close.
func (c *Controller) Done() <-chan struct{} { return c.stopped }

// Shutdown requests idempotent shutdown and waits for owned transports and
// goroutines. The wait honors ctx; shutdown continues if ctx expires.
func (c *Controller) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidOperation
	}
	c.closeOnce.Do(func() { c.cancel(terminalCause(playback.TerminalShutdown)) })
	select {
	case <-c.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close shuts down the Controller and waits without a deadline. It is safe to
// call concurrently and more than once. Prefer Shutdown when blocking must be
// bounded.
func (c *Controller) Close() {
	_ = c.Shutdown(context.Background())
}

func (c *Controller) enqueue(msg message) error {
	select {
	case <-c.ctx.Done():
		return ErrClosed
	default:
	}
	select {
	case c.queue <- msg:
		return nil
	default:
		return ErrBusy
	}
}

func (c *Controller) enqueueInternal(msg message) error {
	select {
	case <-c.ctx.Done():
		return ErrClosed
	case c.queue <- msg:
		return nil
	}
}

// Snapshot returns a detached state projection. It returns context errors,
// ErrBusy when the actor queue is saturated, or ErrClosed after shutdown.
func (c *Controller) Snapshot(ctx context.Context) (Snapshot, error) {
	if ctx == nil {
		return Snapshot{}, ErrInvalidOperation
	}
	var snapshot Snapshot
	_, err := c.callActor(ctx, func(s *actorState) { snapshot = s.snapshot() })
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// callActor lets either cancellation or actor execution claim the call. Once
// execution starts, the caller waits for its single outcome instead of racing.
func (c *Controller) callActor(ctx context.Context, fn func(*actorState)) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var revision uint64
	done := make(chan struct{})
	call := &actorCall{}
	err := c.enqueue(message{done: done, call: call, fn: func(s *actorState) {
		revision = s.revision
		fn(s)
	}})
	if err != nil {
		return 0, err
	}
	select {
	case <-done:
		return revision, nil
	case <-ctx.Done():
		if call.state.CompareAndSwap(actorCallPending, actorCallCanceled) {
			return 0, ctx.Err()
		}
		select {
		case <-done:
			return revision, nil
		case <-c.done:
			select {
			case <-done:
				return revision, nil
			default:
				return 0, ErrClosed
			}
		}
	case <-c.done:
		select {
		case <-done:
			return revision, nil
		default:
			return 0, ErrClosed
		}
	}
}

// callResult prevents a canceled queued command from starting. Once the actor
// accepts the command, its single committed outcome wins the cancellation race.
func (c *Controller) callResult(ctx context.Context, requestID string, fn func(*actorState, chan<- Result)) Result {
	if err := ctx.Err(); err != nil {
		return fail(requestID, 0, err)
	}
	response := make(chan Result, 1)
	call := &actorCall{}
	if err := c.enqueue(message{call: call, fn: func(s *actorState) { fn(s, response) }}); err != nil {
		return fail(requestID, 0, err)
	}
	select {
	case result := <-response:
		return result
	case <-ctx.Done():
		if call.state.CompareAndSwap(actorCallPending, actorCallCanceled) {
			return fail(requestID, 0, ctx.Err())
		}
		select {
		case result := <-response:
			return result
		case <-c.stopped:
			select {
			case result := <-response:
				return result
			default:
				return fail(requestID, 0, ErrClosed)
			}
		}
	case <-c.stopped:
		select {
		case result := <-response:
			return result
		default:
			return fail(requestID, 0, ErrClosed)
		}
	}
}

func (s *actorState) snapshot() Snapshot {
	result := Snapshot{
		Revision: s.revision, Devices: slices.Clone(s.devices), SelectedDeviceID: s.selectedID,
		SelectedMedia: s.media.Name, SelectedSubtitle: s.subtitle.Name, Transcode: s.transcode,
		Generation: s.generation, PlaybackState: s.state, Position: s.position, Duration: s.duration,
		Volume: s.volume, Muted: s.muted, ArtworkID: s.artworkID, Policy: s.policy,
		LastError: s.lastError, TerminalReason: s.terminal,
	}
	if s.active != nil {
		result.HasSession, result.ActiveDeviceID, result.ActiveMediaName, result.MediaType = true, s.active.target.ID, s.active.media.Name, s.active.kind
	}
	if s.queue != nil {
		current, _ := s.queue.Current()
		for _, item := range s.queue.Items() {
			active := s.active != nil && item.ID() == s.active.itemID
			result.Queue = append(result.Queue, QueueItem{ID: item.ID(), Name: item.BaseName(), Parent: item.ParentFolder(), MediaKind: item.MediaKind(), IsSelected: item.ID() == current.ID(), IsActive: active})
		}
	}
	return result
}

func (s *actorState) setDevices(devices []playback.Device) {
	s.devices = slices.Clone(devices)
	if s.selectedID != "" && !slices.ContainsFunc(devices, func(device playback.Device) bool { return device.ID == s.selectedID }) {
		s.selectedID = ""
		s.commit()
		return
	}
	s.commit()
}

func (s *actorState) commit() { s.revision++ }

func (s *actorState) check(m Mutation) Result {
	if m.ExpectedRevision != nil && *m.ExpectedRevision != s.revision {
		return fail(m.RequestID, s.revision, ErrConflict)
	}
	return Result{RequestID: m.RequestID, Revision: s.revision}
}

func fail(requestID string, revision uint64, err error) Result {
	code, message := ErrorCodeOf(err), "operation failed"
	switch {
	case code == CodeBusy:
		message = "operation busy"
	case code == CodeConflict:
		message = "state changed"
	case code == CodeNotFound:
		message = "not found"
	case code == CodeNoDevice:
		message = "select a device"
	case code == CodeNoMedia:
		message = "select media"
	case code == CodeNoSession:
		message = "nothing playing"
	case code == CodeAudioOnly:
		message = "device supports audio only"
	case code == CodeQueueLimit:
		message = "queue too large"
	case errors.Is(err, ErrSeekUnsupported), errors.Is(err, playback.ErrSeekUnsupported):
		message = "seek unsupported"
	case errors.Is(err, playback.ErrSeekNegative), errors.Is(err, playback.ErrSeekPastDuration):
		message = "invalid seek position"
	case code == CodeInvalid:
		message = "invalid request"
	case code == CodeClosed:
		message = "controller closed"
	case code == CodeCanceled:
		message = "operation canceled"
	case code == CodeDeadline:
		message = "operation deadline exceeded"
	}
	return Result{RequestID: requestID, Revision: revision, Code: code, Message: message}
}

func (c *Controller) mutate(ctx context.Context, mutation Mutation, fn func(*actorState) Result) Result {
	if ctx == nil {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	result := Result{RequestID: mutation.RequestID}
	revision, err := c.callActor(ctx, func(s *actorState) { result = fn(s) })
	if err != nil {
		return fail(mutation.RequestID, revision, err)
	}
	return result
}

// SelectDevice selects a currently discovered device by opaque ID.
func (c *Controller) SelectDevice(ctx context.Context, mutation Mutation, id string) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		if !slices.ContainsFunc(s.devices, func(device playback.Device) bool { return device.ID == id }) {
			return fail(mutation.RequestID, s.revision, ErrNotFound)
		}
		s.selectedID = id
		s.commit()
		return Result{RequestID: mutation.RequestID, Revision: s.revision}
	})
}

// SelectMedia validates, copies, and selects an in-process media reference.
func (c *Controller) SelectMedia(ctx context.Context, mutation Mutation, media MediaRef) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		if !media.valid() {
			return fail(mutation.RequestID, s.revision, ErrInvalidOperation)
		}
		s.media = cloneMediaRef(media)
		s.mediaQueueID = ""
		s.artworkID = mediaArtworkID(media)
		s.commit()
		return Result{RequestID: mutation.RequestID, Revision: s.revision}
	})
}

func cloneMediaRef(media MediaRef) MediaRef {
	if media.Artwork == nil {
		return media
	}
	artwork := *media.Artwork
	artwork.Data = bytes.Clone(media.Artwork.Data)
	media.Artwork = &artwork
	return media
}

// SelectSubtitle selects a subtitle reference. The zero reference clears it.
func (c *Controller) SelectSubtitle(ctx context.Context, mutation Mutation, subtitle SubtitleRef) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		if subtitle.Validate() != nil {
			return fail(mutation.RequestID, s.revision, ErrInvalidOperation)
		}
		s.subtitle = subtitle
		s.commit()
		return Result{RequestID: mutation.RequestID, Revision: s.revision}
	})
}

// SetTranscode controls transcoding for subsequent loads.
func (c *Controller) SetTranscode(ctx context.Context, mutation Mutation, enabled bool) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		s.transcode = enabled
		s.commit()
		return Result{RequestID: mutation.RequestID, Revision: s.revision}
	})
}

// SetArtworkID changes the display artwork cache ID without changing media.
func (c *Controller) SetArtworkID(ctx context.Context, mutation Mutation, id string) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		s.artworkID = strings.TrimSpace(id)
		s.commit()
		return Result{RequestID: mutation.RequestID, Revision: s.revision}
	})
}

// AddQueueItem validates Media, then returns the existing absolute-path match
// or appends a copied item.
func (c *Controller) AddQueueItem(ctx context.Context, request QueueAddRequest) QueueAddResult {
	var itemID string
	result := c.mutate(ctx, request.Mutation, func(s *actorState) Result {
		if result := s.check(request.Mutation); !result.OK() {
			return result
		}
		if !request.Media.valid() {
			return fail(request.RequestID, s.revision, ErrInvalidOperation)
		}
		if item, ok := s.queueItemByMedia(request.Media); ok {
			itemID = item.ID()
			if request.Select {
				s.selectQueueMedia(item, request.Media)
				s.commit()
			}
			return Result{RequestID: request.RequestID, Revision: s.revision}
		}
		if s.queue != nil && s.queue.Len() >= MaxQueueItems {
			return fail(request.RequestID, s.revision, ErrQueueLimit)
		}
		item, ok := mediamodel.NewQueueReference(request.Media.ID, request.Media.Name, request.Media.Parent, request.Media.Kind)
		if !ok {
			return fail(request.RequestID, s.revision, ErrInvalidOperation)
		}
		s.appendQueueItem(item, request.Media, request.Select)
		itemID = item.ID()
		s.commit()
		return Result{RequestID: request.RequestID, Revision: s.revision}
	})
	return QueueAddResult{Result: result, ItemID: itemID}
}

func (s *actorState) queueItemByMedia(media MediaRef) (mediamodel.QueueItem, bool) {
	if s.queue == nil {
		return mediamodel.QueueItem{}, false
	}
	for _, item := range s.queue.Items() {
		if queued, ok := s.queueRefs[item.ID()]; ok && sameMediaPath(queued, media) {
			return item, true
		}
	}
	return mediamodel.QueueItem{}, false
}

func sameMediaPath(a, b MediaRef) bool {
	if filepath.IsAbs(a.AbsolutePath) && filepath.IsAbs(b.AbsolutePath) {
		left, right := filepath.Clean(a.AbsolutePath), filepath.Clean(b.AbsolutePath)
		if runtime.GOOS == "windows" {
			return strings.EqualFold(left, right)
		}
		return left == right
	}
	return a.RootID == b.RootID && a.ID == b.ID
}

func (s *actorState) selectQueueMedia(item mediamodel.QueueItem, media MediaRef) {
	s.queue.SetCurrentIndex(queueIndex(s.queue, item.ID()))
	media = cloneMediaRef(media)
	s.queueRefs[item.ID()] = media
	s.media = media
	s.mediaQueueID = item.ID()
	s.artworkID = mediaArtworkID(media)
}

func (s *actorState) appendQueueItem(item mediamodel.QueueItem, media MediaRef, selectItem bool) {
	media = cloneMediaRef(media)
	items, current := []mediamodel.QueueItem{item}, -1
	if s.queue != nil {
		items = append(s.queue.Items(), item)
		current = s.queue.CurrentIndex()
	}
	if selectItem {
		current = len(items) - 1
	}
	s.queue = mediamodel.NewQueue(items, current)
	if s.queueRefs == nil {
		s.queueRefs = make(map[string]MediaRef)
	}
	s.queueRefs[item.ID()] = media
	if selectItem {
		s.media = media
		s.mediaQueueID = item.ID()
		s.artworkID = mediaArtworkID(media)
	}
}

// ClearQueue removes queued items. An active queued item is retained until its
// session ends so snapshots and autoplay retain a valid identity.
func (c *Controller) ClearQueue(ctx context.Context, mutation Mutation) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		if s.active != nil {
			if index := queueIndex(s.queue, s.active.itemID); index >= 0 {
				item, _ := s.queue.Item(index)
				s.queue = mediamodel.NewQueue([]mediamodel.QueueItem{item}, 0)
				s.queueRefs = map[string]MediaRef{item.ID(): s.active.media}
				s.media = s.active.media
				s.mediaQueueID = item.ID()
				s.artworkID = mediaArtworkID(s.active.media)
				s.commit()
				return Result{RequestID: mutation.RequestID, Revision: s.revision}
			}
		}
		s.queue = nil
		s.queueRefs = nil
		if s.mediaQueueID != "" {
			s.media, s.mediaQueueID = MediaRef{}, ""
			s.artworkID = ""
		}
		s.position, s.duration = 0, 0
		s.commit()
		return Result{RequestID: mutation.RequestID, Revision: s.revision}
	})
}

// SelectQueueItem selects an existing item by its opaque queue ID.
func (c *Controller) SelectQueueItem(ctx context.Context, mutation Mutation, id string) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		index := queueIndex(s.queue, id)
		media, ok := s.queueRefs[id]
		if index < 0 || !ok {
			return fail(mutation.RequestID, s.revision, ErrNotFound)
		}
		s.queue.SetCurrentIndex(index)
		s.media = media
		s.mediaQueueID = id
		s.artworkID = mediaArtworkID(media)
		s.commit()
		return Result{RequestID: mutation.RequestID, Revision: s.revision}
	})
}

// RemoveQueueItem removes a non-selected, inactive item.
func (c *Controller) RemoveQueueItem(ctx context.Context, mutation Mutation, id string) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		index := queueIndex(s.queue, id)
		if index < 0 {
			return fail(mutation.RequestID, s.revision, ErrNotFound)
		}
		selected, _ := s.queue.Current()
		if selected.ID() == id || s.active != nil && s.active.itemID == id {
			return fail(mutation.RequestID, s.revision, ErrInvalidOperation)
		}
		s.queue.Remove(index)
		delete(s.queueRefs, id)
		if s.mediaQueueID == id {
			s.media, s.mediaQueueID = MediaRef{}, ""
			s.artworkID = ""
		}
		s.commit()
		return Result{RequestID: mutation.RequestID, Revision: s.revision}
	})
}

// MoveQueueItem moves an item by delta.
func (c *Controller) MoveQueueItem(ctx context.Context, mutation Mutation, id string, delta int) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		index := queueIndex(s.queue, id)
		if index < 0 {
			return fail(mutation.RequestID, s.revision, ErrNotFound)
		}
		if delta == 0 || s.queue.Move(index, delta) == index {
			return fail(mutation.RequestID, s.revision, ErrInvalidOperation)
		}
		s.commit()
		return Result{RequestID: mutation.RequestID, Revision: s.revision}
	})
}

// SetPolicy validates and atomically replaces the playback policy.
func (c *Controller) SetPolicy(ctx context.Context, request PolicyRequest) Result {
	return c.mutate(ctx, request.Mutation, func(s *actorState) Result {
		if result := s.check(request.Mutation); !result.OK() {
			return result
		}
		p := request.Policy
		if p.Validate() != nil {
			return fail(request.RequestID, s.revision, ErrInvalidPolicy)
		}
		if p.GaplessEnabled {
			device, ok := s.selectedDevice()
			if s.active != nil {
				device, ok = s.active.target, true
			}
			if !ok || !strings.EqualFold(device.Protocol, "DLNA") {
				return fail(request.RequestID, s.revision, ErrInvalidPolicy)
			}
		}
		restartImageTimer := s.policy.AutoPlayNext != p.AutoPlayNext || s.policy.ImageDurationSeconds != p.ImageDurationSeconds
		s.policy = p
		if restartImageTimer {
			s.syncImageTimer()
		}
		s.commit()
		return Result{RequestID: request.RequestID, Revision: s.revision}
	})
}

func queueIndex(queue *mediamodel.Queue, id string) int {
	if queue == nil {
		return -1
	}
	return slices.IndexFunc(queue.Items(), func(item mediamodel.QueueItem) bool { return item.ID() == id })
}

func (s *actorState) selectedDevice() (playback.Device, bool) {
	for _, device := range s.devices {
		if device.ID == s.selectedID {
			return device, true
		}
	}
	return playback.Device{}, false
}

func mediaMIME(media MediaRef, kind mediamodel.MediaKind) string {
	if strings.TrimSpace(media.MIMEType) != "" {
		return media.MIMEType
	}
	switch kind {
	case mediamodel.MediaKindAudio:
		return "audio/*"
	case mediamodel.MediaKindVideo:
		return "video/*"
	case mediamodel.MediaKindImage:
		return "image/*"
	default:
		return "application/octet-stream"
	}
}

func mediaArtworkID(media MediaRef) string {
	if media.Artwork == nil {
		return ""
	}
	return media.Artwork.ID
}

func (s *actorState) choosePlay(request PlayRequest) (playback.Device, mediamodel.QueueItem, MediaRef, error) {
	target, ok := s.selectedDevice()
	if request.target != nil {
		target, ok = *request.target, true
	}
	if !ok {
		return target, mediamodel.QueueItem{}, MediaRef{}, ErrNoDevice
	}
	var item mediamodel.QueueItem
	var resolved MediaRef
	if request.media != nil {
		if !request.media.valid() {
			return target, item, MediaRef{}, ErrNoMedia
		}
		resolved = *request.media
		item, ok = mediamodel.NewQueueReference(resolved.ID, resolved.Name, resolved.Parent, resolved.Kind)
		if !ok {
			return target, item, MediaRef{}, ErrNoMedia
		}
	} else if request.QueueItemID != "" {
		index := queueIndex(s.queue, request.QueueItemID)
		if index < 0 {
			return target, item, MediaRef{}, ErrNotFound
		}
		item, _ = s.queue.Item(index)
	} else if s.mediaQueueID != "" {
		index := queueIndex(s.queue, s.mediaQueueID)
		if index < 0 {
			return target, item, MediaRef{}, ErrNotFound
		}
		item, _ = s.queue.Item(index)
	} else if s.media.valid() {
		item, _ = mediamodel.NewQueueReference(s.media.ID, s.media.Name, s.media.Parent, s.media.Kind)
	} else if s.queue != nil {
		item, ok = s.queue.Current()
		if !ok {
			return target, item, MediaRef{}, ErrNoMedia
		}
	} else {
		return target, item, MediaRef{}, ErrNoMedia
	}
	if target.AudioOnly && item.MediaKind() != mediamodel.MediaKindAudio {
		return target, item, MediaRef{}, ErrAudioOnly
	}
	if request.media != nil {
		return target, item, resolved, nil
	}
	media, ok := s.queueRefs[item.ID()]
	if request.QueueItemID == "" && s.mediaQueueID == "" && s.media.valid() {
		media, ok = s.media, true
	}
	if !ok {
		return target, item, MediaRef{}, ErrNoMedia
	}
	return target, item, media, nil
}

// Play loads and starts the requested or selected media. Controller owns the
// opened transport until replacement, stop, failure, or shutdown.
func (c *Controller) Play(ctx context.Context, request PlayRequest) Result {
	return c.play(ctx, request)
}

// QueueAndPlay atomically adds or reuses, selects, and starts media.
func (c *Controller) QueueAndPlay(ctx context.Context, mutation Mutation, media MediaRef) Result {
	media = cloneMediaRef(media)
	return c.play(ctx, PlayRequest{Mutation: mutation, media: &media, queueMedia: true})
}

func (c *Controller) play(ctx context.Context, request PlayRequest) Result {
	if ctx == nil {
		return fail(request.RequestID, 0, ErrInvalidOperation)
	}
	request.ctx = ctx
	return c.callResult(ctx, request.RequestID, func(s *actorState, response chan<- Result) {
		s.beginPlay(request, response)
	})
}

func (s *actorState) beginPlay(request PlayRequest, response chan<- Result) {
	if result := s.check(request.Mutation); !result.OK() {
		response <- result
		return
	}
	if s.mutation {
		response <- fail(request.RequestID, s.revision, ErrBusy)
		return
	}
	target, item, media, err := s.choosePlay(request)
	if err != nil {
		response <- fail(request.RequestID, s.revision, err)
		return
	}
	if s.controller.cfg.TransportFactory == nil || s.controller.cfg.MediaServer == nil {
		response <- fail(request.RequestID, s.revision, ErrInvalidOperation)
		return
	}
	if request.queueMedia {
		if existing, ok := s.queueItemByMedia(media); ok {
			item = existing
			s.selectQueueMedia(item, media)
		} else {
			if s.queue != nil && s.queue.Len() >= MaxQueueItems {
				response <- fail(request.RequestID, s.revision, ErrQueueLimit)
				return
			}
			s.appendQueueItem(item, media, true)
		}
	}
	s.mutation = true
	s.deferred = nil
	s.state = PlaybackStateLoading
	s.generation++
	generation := s.generation
	old := s.active
	if old != nil {
		old.cancel(terminalCause(playback.TerminalReplacement))
		s.terminal = playback.TerminalReplacement
	}
	if index := queueIndex(s.queue, item.ID()); index >= 0 {
		s.queue.SetCurrentIndex(index)
		s.mediaQueueID = item.ID()
	} else {
		s.mediaQueueID = ""
	}
	s.media = media
	s.artworkID = mediaArtworkID(media)
	s.commit()
	opCtx, cancel := context.WithCancelCause(s.controller.ctx)
	operation := &playOperation{generation: generation, cancel: cancel, done: make(chan struct{})}
	s.pending = operation
	s.controller.goOwned(func() {
		s.controller.playIO(opCtx, operation, target, item, media, old, request, response, s.transcode, s.subtitle)
	})
}

type playCompletion struct {
	generation uint64
	operation  *playOperation
	session    *activeSession
	err        error
	request    PlayRequest
	response   chan<- Result
}

type existingLoader interface {
	LoadOnExisting(context.Context, playback.LoadRequest) error
}

type mediaRouteAdder interface {
	AddMedia(context.Context, playback.ServerRequest) (playback.MediaRoute, error)
}

type callbackActivator interface {
	ActivateCallbacks(uint64) error
}

type callbackStopSuppressor interface {
	SuppressCallbackStops(uint64, bool) error
}

func (c *Controller) playIO(ctx context.Context, operation *playOperation, target playback.Device, item mediamodel.QueueItem, media MediaRef, old *activeSession, request PlayRequest, response chan<- Result, transcode bool, subtitle SubtitleRef) {
	generation := operation.generation
	ioCtx, timeoutCancel := operationContext(ctx, request.ctx, c.cfg.OperationTimeout)
	defer timeoutCancel()
	if target.Protocol == "Chromecast" {
		transcode = playback.ChromecastTranscodeEnabled(transcode, media.Name, mediaMIME(media, item.MediaKind()))
	}
	var reusedCast existingLoader
	var routeAdder mediaRouteAdder
	if old != nil {
		if target.Protocol == "Chromecast" && old.target.ID == target.ID && old.reusable {
			reusedCast, _ = old.transport.(existingLoader)
		}
		if reusedCast != nil && !old.server.Transcode && old.server.Subtitle == nil && !transcode && !subtitle.valid() {
			routeAdder, _ = c.cfg.MediaServer.(mediaRouteAdder)
		}
		var err error
		switch {
		case routeAdder != nil:
		case reusedCast != nil:
			err = c.cfg.MediaServer.Stop(ioCtx)
		default:
			err = transitionSession(ioCtx, old, c.cfg.MediaServer, c.cfg.OperationTimeout)
		}
		if err != nil {
			if reusedCast != nil {
				_ = cleanupTransport(old.transport, c.cfg.MediaServer, c.cfg.OperationTimeout)
			} else {
				_ = cleanupMediaServer(c.cfg.MediaServer, c.cfg.OperationTimeout)
			}
			c.completePlay(playCompletion{generation: generation, operation: operation, err: err, request: request, response: response})
			return
		}
	}
	if err := ioCtx.Err(); err != nil {
		if reusedCast != nil {
			_ = cleanupTransport(old.transport, c.cfg.MediaServer, c.cfg.OperationTimeout)
		}
		c.completePlay(playCompletion{generation: generation, operation: operation, err: err, request: request, response: response})
		return
	}
	var transport Transport
	var err error
	if reusedCast != nil {
		transport = old.transport
	} else {
		transport, err = c.cfg.TransportFactory.Open(ioCtx, target)
		if err != nil {
			c.completePlay(playCompletion{generation: generation, operation: operation, err: err, request: request, response: response})
			return
		}
	}
	if transport == nil {
		c.completePlay(playCompletion{generation: generation, operation: operation, err: fmt.Errorf("transport factory returned nil: %w", errAdapterContract), request: request, response: response})
		return
	}
	opener := media.OpenDirect
	if transcode {
		opener = media.OpenTranscode
		if opener == nil {
			_ = cleanupTransport(transport, c.cfg.MediaServer, c.cfg.OperationTimeout)
			c.completePlay(playCompletion{generation: generation, operation: operation, err: ErrInvalidOperation, request: request, response: response})
			return
		}
	}
	serverRequest := playback.ServerRequest{Media: opener, MediaExt: media.extension(), MediaType: mediaMIME(media, item.MediaKind()), Transcode: transcode, Target: target}
	if subtitle.valid() {
		serverRequest.Subtitle, serverRequest.SubtitleExt = subtitle.Open, subtitle.extension()
	}
	duration := 0.0
	if transcode && target.Protocol == "Chromecast" && c.cfg.DurationProbe != nil {
		if probed, probeErr := c.cfg.DurationProbe(ioCtx, media.OpenDirect); probeErr == nil && probed > 0 {
			duration = probed
		}
	}
	var route playback.MediaRoute
	if routeAdder != nil {
		route, err = routeAdder.AddMedia(ioCtx, serverRequest)
	} else {
		route, err = c.cfg.MediaServer.Start(ioCtx, serverRequest)
	}
	if err == nil && strings.TrimSpace(route.URL) == "" {
		err = fmt.Errorf("media server returned empty URL: %w", errAdapterContract)
	}
	routeIDs := make([]string, 0, 2)
	if route.ID != "" {
		routeIDs = append(routeIDs, route.ID)
	}
	loadMediaType := mediaMIME(media, item.MediaKind())
	if transcode && target.Protocol == "Chromecast" {
		loadMediaType = "video/mp4"
	}
	loadRequest := playback.LoadRequest{MediaURL: route.URL, MediaType: loadMediaType, SubtitleURL: route.SubtitleURL, Duration: duration, Seekable: !transcode, Metadata: metadata.Media{Title: item.BaseName()}}
	if media.Artwork != nil {
		loadRequest.ArtworkData = append([]byte(nil), media.Artwork.Data...)
		artRoute, artErr := c.cfg.MediaServer.Add(ioCtx, playback.RouteRequest{MediaType: media.Artwork.MIMEType, Contents: loadRequest.ArtworkData})
		if artErr == nil && strings.TrimSpace(artRoute.URL) != "" {
			loadRequest.Metadata.Artwork = &metadata.Artwork{URL: artRoute.URL, MIMEType: media.Artwork.MIMEType, Width: media.Artwork.Width, Height: media.Artwork.Height}
			if artRoute.ID != "" {
				routeIDs = append(routeIDs, artRoute.ID)
			}
		}
	}
	if err == nil && target.Protocol == "DLNA" {
		if activator, ok := transport.(callbackActivator); ok {
			err = activator.ActivateCallbacks(generation)
		}
	}
	if err == nil && reusedCast != nil {
		err = reusedCast.LoadOnExisting(ioCtx, loadRequest)
		if err != nil && ioCtx.Err() == nil {
			if c.cfg.Logger != nil {
				c.cfg.Logger.Debug("Chromecast receiver reuse failed; retrying fresh connection: " + err.Error())
			}
			_ = rendererCleanup(transport, c.cfg.OperationTimeout, func(ctx context.Context, transport Transport) error {
				return transport.Close(ctx)
			})
			transport, err = c.cfg.TransportFactory.Open(ioCtx, target)
			if err == nil && transport == nil {
				err = fmt.Errorf("transport factory returned nil: %w", errAdapterContract)
			}
			if err == nil {
				err = transport.Load(ioCtx, loadRequest)
			}
		}
	} else if err == nil {
		err = transport.Load(ioCtx, loadRequest)
	}
	if err == nil && routeAdder != nil {
		for _, id := range old.routeIDs {
			if id == "" || slices.Contains(routeIDs, id) {
				continue
			}
			if removeErr := c.cfg.MediaServer.Remove(ioCtx, id); removeErr != nil && c.cfg.Logger != nil {
				c.cfg.Logger.Debug("Chromecast old media route cleanup failed: " + removeErr.Error())
			}
		}
	}
	// Chromecast LOAD autoplays. Calling Play before its media session status
	// arrives fails with "media not yet initialised" and tears down a good load.
	if err == nil && target.Protocol != "Chromecast" {
		err = transport.Play(ioCtx)
	}
	if err != nil {
		_ = cleanupTransport(transport, c.cfg.MediaServer, c.cfg.OperationTimeout)
		c.completePlay(playCompletion{generation: generation, operation: operation, err: err, request: request, response: response})
		return
	}
	c.completePlay(playCompletion{generation: generation, operation: operation, session: &activeSession{generation: generation, target: target, itemID: item.ID(), media: media, subtitle: subtitle, kind: item.MediaKind(), transport: transport, server: serverRequest, load: loadRequest, routeIDs: routeIDs, ctx: ctx, cancel: operation.cancel, reusable: true, imageReady: target.Protocol != "Chromecast", expectedDuration: int(loadRequest.Duration)}, request: request, response: response})
}

func operationContext(base, request context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if base == nil {
		base = context.Background()
	}
	if request == nil {
		request = base
	}
	ctx, cancel := context.WithTimeout(request, timeout)
	stopBase := context.AfterFunc(base, cancel)
	return ctx, func() {
		stopBase()
		cancel()
	}
}

func (c *Controller) startMonitor(session *activeSession) {
	if c.cfg.RunMonitor == nil || session == nil {
		return
	}
	cfg := playback.MonitorConfig{
		Generation:       session.generation,
		SeekOffset:       session.seekOffset,
		ExpectedDuration: session.expectedDuration,
		Clock:            c.cfg.Clock,
		Sink:             c,
	}
	c.goOwned(func() { c.cfg.RunMonitor(session.ctx, cfg, session.target, session.transport) })
}

func rendererCleanupTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return time.Second
	}
	step := timeout / 4
	if step <= 0 {
		step = timeout
	}
	return min(step, time.Second)
}

func rendererCleanup(transport Transport, timeout time.Duration, call func(context.Context, Transport) error) error {
	if transport == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), rendererCleanupTimeout(timeout))
	defer cancel()
	return call(ctx, transport)
}

func cleanupTransport(transport Transport, server playback.MediaServer, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return teardownSession(ctx, &activeSession{transport: transport}, server, timeout)
}

func cleanupMediaServer(server playback.MediaServer, timeout time.Duration) error {
	if server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return server.Stop(ctx)
}

func teardownSession(ctx context.Context, session *activeSession, server playback.MediaServer, closeTimeout time.Duration) error {
	if session == nil {
		return nil
	}
	var result error
	if session.target.Protocol != "Chromecast" || session.reusable {
		result = errors.Join(result, rendererCleanup(session.transport, closeTimeout, func(ctx context.Context, transport Transport) error {
			return transport.Stop(ctx)
		}))
	}
	if server != nil {
		result = errors.Join(result, server.Stop(ctx))
	}
	result = errors.Join(result, rendererCleanup(session.transport, closeTimeout, func(ctx context.Context, transport Transport) error {
		return transport.Close(ctx)
	}))
	return result
}

// Natural completion can leave a renderer already stopped. Desktop playback
// treats renderer Stop/Close errors as cleanup noise and still loads the next
// queue item; only failure to release the media server blocks the transition.
func transitionSession(ctx context.Context, session *activeSession, server playback.MediaServer, closeTimeout time.Duration) error {
	if session == nil {
		return nil
	}
	if session.target.Protocol != "Chromecast" || session.reusable {
		_ = rendererCleanup(session.transport, closeTimeout, func(ctx context.Context, transport Transport) error {
			return transport.Stop(ctx)
		})
	}
	err := server.Stop(ctx)
	_ = rendererCleanup(session.transport, closeTimeout, func(ctx context.Context, transport Transport) error {
		return transport.Close(ctx)
	})
	return err
}

func (c *Controller) completePlay(completion playCompletion) {
	ack := make(chan struct{})
	var actorResponded, cleanupOwned, stale bool
	var revision uint64
	err := c.enqueueInternal(message{done: ack, fn: func(s *actorState) {
		revision = s.revision
		if completion.operation != s.pending || completion.generation != s.generation {
			stale = true
			cleanupOwned = completion.operation.claim(playOperationCleanup)
			return
		}
		if completion.err != nil {
			if !completion.operation.claim(playOperationCleanup) {
				stale = true
				return
			}
			s.pending = nil
			s.mutation = false
			s.active, s.state, s.lastError, s.terminal = nil, PlaybackStateStopped, "playback failed", playback.TerminalError
			s.commit()
			if c.cfg.Logger != nil {
				c.cfg.Logger.Error("Playback failed")
				c.cfg.Logger.Debug("Playback failure detail: " + completion.err.Error())
			}
			completion.operation.finish()
			actorResponded = true
			completion.response <- fail(completion.request.RequestID, s.revision, completion.err)
			return
		}
		if !completion.operation.claim(playOperationAccepted) {
			stale = true
			return
		}
		s.pending = nil
		s.mutation = false
		s.active, s.state, s.position, s.duration, s.lastError, s.terminal = completion.session, PlaybackStatePlaying, 0, 0, "", ""
		s.syncImageTimer()
		s.commit()
		if c.cfg.Logger != nil {
			c.cfg.Logger.Info("Playback started: " + completion.session.media.Name)
		}
		completion.operation.finish()
		c.startMonitor(completion.session)
		actorResponded = true
		completion.response <- Result{RequestID: completion.request.RequestID, Revision: s.revision}
	}})
	if err == nil {
		select {
		case <-ack:
		case <-c.done:
			select {
			case <-ack:
			default:
				err = ErrClosed
			}
		}
	}
	if actorResponded {
		return
	}
	if cleanupOwned || (err != nil && completion.operation.claim(playOperationCleanup)) {
		if completion.session != nil {
			completion.session.cancel(terminalCause(playback.TerminalReplacement))
			_ = cleanupTransport(completion.session.transport, c.cfg.MediaServer, c.cfg.OperationTimeout)
		}
		completion.operation.finish()
	}
	if err != nil {
		completion.response <- fail(completion.request.RequestID, 0, err)
		return
	}
	if stale {
		completion.response <- fail(completion.request.RequestID, revision, ErrBusy)
	}
}

func (s *actorState) syncImageTimer() {
	if s.active == nil {
		return
	}
	if s.active.imageTimer != nil {
		s.active.imageTimer()
		s.active.imageTimer = nil
	}
	s.active.imageEpoch++
	if s.active.kind != mediamodel.MediaKindImage || !s.active.imageReady || !s.policy.AutoPlayNext || s.policy.ImageDurationSeconds <= 0 {
		return
	}
	timerCtx, cancel := context.WithCancel(s.active.ctx)
	s.active.imageTimer = cancel
	generation := s.active.generation
	timerEpoch := s.active.imageEpoch
	delay := time.Duration(s.policy.ImageDurationSeconds) * time.Second
	s.controller.goOwned(func() {
		playback.RunImageTimer(timerCtx, playback.MonitorConfig{Generation: generation, TimerEpoch: timerEpoch, Clock: s.controller.cfg.Clock, Sink: s.controller}, delay)
	})
}

// Stop ends pending or active playback and completes owned cleanup before
// returning. Cleanup continues if the caller's context is canceled.
func (c *Controller) Stop(ctx context.Context, mutation Mutation) Result {
	if ctx == nil {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	return c.callResult(ctx, mutation.RequestID, func(s *actorState, response chan<- Result) {
		if result := s.check(mutation); !result.OK() {
			response <- result
			return
		}
		if s.cleanup {
			response <- fail(mutation.RequestID, s.revision, ErrBusy)
			return
		}
		s.generation++
		s.deferred = nil
		s.terminal, s.state = playback.TerminalUserStop, PlaybackStateStopping
		active := s.active
		pending := s.pending
		if pending != nil {
			pending.cancel(terminalCause(playback.TerminalUserStop))
		} else if active != nil {
			active.cancel(terminalCause(playback.TerminalUserStop))
		}
		s.active, s.pending, s.mutation, s.cleanup = nil, nil, true, true
		s.position, s.duration = 0, 0
		if pending == nil && active == nil {
			s.mutation, s.cleanup, s.state = false, false, PlaybackStateStopped
			s.commit()
			response <- Result{RequestID: mutation.RequestID, Revision: s.revision}
			return
		}
		s.commit()
		generation := s.generation
		c.goOwned(func() {
			var err error
			if pending != nil {
				<-pending.done
			} else {
				ioCtx, cancel := context.WithTimeout(context.Background(), c.cfg.OperationTimeout)
				err = teardownSession(ioCtx, active, c.cfg.MediaServer, c.cfg.OperationTimeout)
				cancel()
			}
			if enqueueErr := c.enqueueInternal(message{fn: func(s *actorState) {
				if generation != s.generation || !s.cleanup {
					response <- fail(mutation.RequestID, s.revision, ErrBusy)
					return
				}
				s.mutation, s.cleanup, s.state = false, false, PlaybackStateStopped
				if err != nil {
					s.lastError = "stop failed"
				}
				s.commit()
				if err != nil {
					if c.cfg.Logger != nil {
						c.cfg.Logger.Error("Playback stop failed")
						c.cfg.Logger.Debug("Playback stop failure detail: " + err.Error())
					}
					response <- fail(mutation.RequestID, s.revision, err)
				} else {
					if c.cfg.Logger != nil {
						c.cfg.Logger.Info("Playback stopped")
					}
					response <- Result{RequestID: mutation.RequestID, Revision: s.revision}
				}
			}}); enqueueErr != nil {
				response <- fail(mutation.RequestID, 0, enqueueErr)
			}
		})
	})
}

// Pause pauses the active session.
func (c *Controller) Pause(ctx context.Context, mutation Mutation) Result {
	return c.transportControl(ctx, mutation, PlaybackStatePaused, func(ctx context.Context, t Transport) error { return t.Pause(ctx) })
}

// Resume resumes the active session and publishes PlaybackStatePlaying.
func (c *Controller) Resume(ctx context.Context, mutation Mutation) Result {
	return c.transportControl(ctx, mutation, PlaybackStatePlaying, func(ctx context.Context, t Transport) error { return t.Play(ctx) })
}

// Seek moves the active session to an absolute position in seconds.
func (c *Controller) Seek(ctx context.Context, request SeekRequest) Result {
	if ctx == nil {
		return fail(request.RequestID, 0, ErrInvalidOperation)
	}
	return c.callResult(ctx, request.RequestID, func(s *actorState, response chan<- Result) {
		if result := s.check(request.Mutation); !result.OK() {
			response <- result
			return
		}
		if s.mutation {
			response <- fail(request.RequestID, s.revision, ErrBusy)
			return
		}
		if s.active == nil {
			response <- fail(request.RequestID, s.revision, ErrNoSession)
			return
		}
		if s.duration <= 0 {
			response <- fail(request.RequestID, s.revision, ErrSeekUnsupported)
			return
		}
		if err := playback.ValidateSeek(request.Seconds, s.duration); err != nil {
			response <- fail(request.RequestID, s.revision, err)
			return
		}
		active := s.active
		var dlna playback.DLNATransport
		var cast playback.ChromecastTransport
		switch active.target.Protocol {
		case "DLNA":
			dlna, _ = active.transport.(playback.DLNATransport)
		case "Chromecast":
			cast, _ = active.transport.(playback.ChromecastTransport)
		}
		if dlna == nil && cast == nil {
			response <- fail(request.RequestID, s.revision, ErrSeekUnsupported)
			return
		}
		// Fence the running monitor before an intentional STOP/reload. Otherwise
		// a transcoded seek can be mistaken for end-of-media and tear down the
		// session while the replacement stream is loading.
		s.mutation = true
		active.cancel(terminalCause(playback.TerminalReplacement))
		s.generation++
		s.deferred = nil
		generation := s.generation
		sessionCtx, sessionCancel := context.WithCancelCause(c.ctx)
		active.ctx, active.cancel, active.generation = sessionCtx, sessionCancel, generation
		duration := s.duration
		c.goOwned(func() {
			ioCtx, cancel := operationContext(c.ctx, ctx, c.cfg.OperationTimeout)
			defer cancel()
			var err error
			if activator, ok := active.transport.(callbackActivator); ok {
				err = activator.ActivateCallbacks(generation)
			}
			var suppressor callbackStopSuppressor
			if err == nil && active.target.Protocol == "DLNA" && active.server.Transcode {
				suppressor, _ = active.transport.(callbackStopSuppressor)
				if suppressor != nil {
					err = suppressor.SuppressCallbackStops(generation, true)
				}
			}
			if err == nil {
				engine := playback.NewSeekEngine(dlna, cast, c.cfg.MediaServer)
				_, err = engine.Seek(ioCtx, playback.SeekRequest{Protocol: active.target.Protocol, Transcoded: active.server.Transcode, Seconds: request.Seconds, Duration: duration, Server: active.server, Load: active.load})
			}
			if suppressor != nil {
				clearErr := suppressor.SuppressCallbackStops(generation, false)
				if err == nil {
					err = clearErr
				}
			}
			if enqueueErr := c.enqueueInternal(message{fn: func(s *actorState) {
				if s.active != active {
					response <- fail(request.RequestID, s.revision, ErrBusy)
					return
				}
				s.mutation = false
				if err != nil {
					s.lastError = "seek failed"
					c.startMonitor(active)
					s.resumeDeferredMonitor()
					response <- fail(request.RequestID, s.revision, err)
					return
				}
				if active.server.Transcode {
					active.server.SeekOffset = request.Seconds
					active.seekOffset = request.Seconds
				}
				s.position, s.state = request.Seconds, PlaybackStatePlaying
				s.commit()
				if c.cfg.Logger != nil {
					c.cfg.Logger.Info("Seeked to " + playbackTime(request.Seconds) + " in " + active.media.Name)
				}
				c.startMonitor(active)
				s.resumeDeferredMonitor()
				response <- Result{RequestID: request.RequestID, Revision: s.revision}
			}}); enqueueErr != nil {
				response <- fail(request.RequestID, 0, enqueueErr)
			}
		})
	})
}

func (c *Controller) transportControl(ctx context.Context, mutation Mutation, state string, call func(context.Context, Transport) error) Result {
	if ctx == nil {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	return c.callResult(ctx, mutation.RequestID, func(s *actorState, response chan<- Result) {
		if result := s.check(mutation); !result.OK() {
			response <- result
			return
		}
		if s.mutation {
			response <- fail(mutation.RequestID, s.revision, ErrBusy)
			return
		}
		if s.active == nil {
			response <- fail(mutation.RequestID, s.revision, ErrNoSession)
			return
		}
		s.mutation = true
		generation, transport := s.generation, s.active.transport
		c.goOwned(func() {
			ioCtx, cancel := operationContext(c.ctx, ctx, c.cfg.OperationTimeout)
			defer cancel()
			err := call(ioCtx, transport)
			if enqueueErr := c.enqueueInternal(message{fn: func(s *actorState) {
				if generation != s.generation {
					response <- fail(mutation.RequestID, s.revision, ErrBusy)
					return
				}
				s.mutation = false
				if err != nil {
					s.lastError = "control failed"
					s.resumeDeferredMonitor()
					response <- fail(mutation.RequestID, s.revision, err)
					return
				}
				previousState := s.state
				s.state = state
				s.commit()
				if c.cfg.Logger != nil && previousState != s.state {
					switch s.state {
					case PlaybackStatePaused:
						c.cfg.Logger.Info("Playback paused at " + playbackTime(s.position))
					case PlaybackStatePlaying:
						c.cfg.Logger.Info("Playback resumed at " + playbackTime(s.position))
					}
				}
				s.resumeDeferredMonitor()
				response <- Result{RequestID: mutation.RequestID, Revision: s.revision}
			}}); enqueueErr != nil {
				response <- fail(mutation.RequestID, 0, enqueueErr)
			}
		})
	})
}

// SetVolume sets renderer volume in the inclusive range 0..100.
func (c *Controller) SetVolume(ctx context.Context, mutation Mutation, volume int) Result {
	if volume < 0 || volume > 100 {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	return c.deviceControl(ctx, mutation, func(ctx context.Context, t Transport) error { return t.SetVolume(ctx, volume) }, func(s *actorState) { s.volume = volume })
}

// AdjustVolume changes renderer volume by exactly -1 or 1 and clamps 0..100.
func (c *Controller) AdjustVolume(ctx context.Context, mutation Mutation, delta int) Result {
	if delta != -1 && delta != 1 {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	volume := 0
	return c.deviceControl(ctx, mutation, func(ctx context.Context, t Transport) error {
		current, err := t.Volume(ctx)
		if err != nil {
			return err
		}
		volume = max(0, min(100, current+delta))
		return t.SetVolume(ctx, volume)
	}, func(s *actorState) { s.volume = volume })
}

// SetMute changes renderer mute state.
func (c *Controller) SetMute(ctx context.Context, mutation Mutation, muted bool) Result {
	return c.deviceControl(ctx, mutation, func(ctx context.Context, t Transport) error { return t.SetMute(ctx, muted) }, func(s *actorState) { s.muted = muted })
}

func (c *Controller) deviceControl(ctx context.Context, mutation Mutation, call func(context.Context, Transport) error, commit func(*actorState)) Result {
	if ctx == nil {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	return c.callResult(ctx, mutation.RequestID, func(s *actorState, response chan<- Result) {
		if result := s.check(mutation); !result.OK() {
			response <- result
			return
		}
		if s.mutation {
			response <- fail(mutation.RequestID, s.revision, ErrBusy)
			return
		}
		var transport Transport
		var target playback.Device
		var transient bool
		if s.active != nil {
			transport = s.active.transport
			if transport == nil {
				response <- fail(mutation.RequestID, s.revision, errAdapterContract)
				return
			}
		} else {
			device, ok := s.selectedDevice()
			if !ok {
				response <- fail(mutation.RequestID, s.revision, ErrNoDevice)
				return
			}
			target, transient = device, true
			if c.cfg.TransportFactory == nil {
				response <- fail(mutation.RequestID, s.revision, ErrInvalidOperation)
				return
			}
		}
		s.mutation = true
		generation := s.generation
		c.goOwned(func() {
			ioCtx, cancel := operationContext(c.ctx, ctx, c.cfg.OperationTimeout)
			defer cancel()
			var err error
			if transient {
				transport, err = c.cfg.TransportFactory.Open(ioCtx, target)
				if err == nil && transport == nil {
					err = fmt.Errorf("transport factory returned nil: %w", errAdapterContract)
				}
			}
			if err == nil {
				err = call(ioCtx, transport)
			}
			if transient && transport != nil {
				err = errors.Join(err, rendererCleanup(transport, c.cfg.OperationTimeout, func(ctx context.Context, transport Transport) error {
					return transport.Close(ctx)
				}))
			}
			if enqueueErr := c.enqueueInternal(message{fn: func(s *actorState) {
				if generation != s.generation {
					response <- fail(mutation.RequestID, s.revision, ErrBusy)
					return
				}
				s.mutation = false
				if err != nil {
					s.resumeDeferredMonitor()
					response <- fail(mutation.RequestID, s.revision, err)
					return
				}
				commit(s)
				s.commit()
				s.resumeDeferredMonitor()
				response <- Result{RequestID: mutation.RequestID, Revision: s.revision}
			}}); enqueueErr != nil {
				response <- fail(mutation.RequestID, 0, enqueueErr)
			}
		})
	})
}

// HandleMonitorEvent accepts adapter monitor updates. Progress events may be
// dropped under pressure; terminal events are retained unless shutting down.
func (c *Controller) HandleMonitorEvent(_ context.Context, event playback.MonitorEvent) {
	if event.Terminal == "" {
		select {
		case <-c.ctx.Done():
		case c.callbacks <- event:
		default:
		}
		return
	}
	select {
	case <-c.ctx.Done():
		return
	case c.callbacks <- event:
	}
}

// HandleCallbackEvent adapts DLNA callback states into monitor events.
func (c *Controller) HandleCallbackEvent(_ context.Context, event httphandlers.CallbackEvent) {
	monitor := playback.MonitorEvent{Generation: event.Generation}
	switch event.TransportState {
	case PlaybackStatePlaying:
		monitor.State = PlaybackStatePlaying
	case "PAUSED_PLAYBACK":
		monitor.State = PlaybackStatePaused
	case PlaybackStateStopped:
		monitor.Terminal = playback.TerminalFinished
	default:
		return
	}
	c.HandleMonitorEvent(context.Background(), monitor)
}

func (s *actorState) monitor(event playback.MonitorEvent) {
	if s.active == nil || event.Generation != s.generation || event.Generation != s.active.generation {
		return
	}
	if event.TimerEpoch != 0 && event.TimerEpoch != s.active.imageEpoch {
		return
	}
	if s.active.target.Protocol == "Chromecast" && event.Err != nil {
		s.active.reusable = false
	}
	if event.Terminal != "" && s.mutation {
		s.deferMonitor(event)
		return
	}
	changed := false
	if event.Position != 0 || event.Duration != 0 {
		s.position, s.duration, changed = event.Position, event.Duration, true
	}
	if event.State != "" {
		previousState := s.state
		s.state, changed = event.State, true
		if s.controller.cfg.Logger != nil && previousState != s.state {
			switch s.state {
			case PlaybackStatePaused:
				s.controller.cfg.Logger.Info("Playback paused at " + playbackTime(s.position))
			case PlaybackStatePlaying:
				if previousState == PlaybackStatePaused {
					s.controller.cfg.Logger.Info("Playback resumed at " + playbackTime(s.position))
				}
			}
		}
	}
	if event.ImageReady && s.active.kind == mediamodel.MediaKindImage && s.active.target.Protocol == "Chromecast" && !s.active.imageReady {
		s.active.imageReady, changed = true, true
		s.syncImageTimer()
	}
	if event.Terminal != "" {
		if s.controller.cfg.Logger != nil {
			switch event.Terminal {
			case playback.TerminalFinished:
				s.controller.cfg.Logger.Info("Playback completed")
			case playback.TerminalError:
				s.controller.cfg.Logger.Error("Playback failed")
				if event.Err != nil {
					s.controller.cfg.Logger.Debug("Playback failure detail: " + event.Err.Error())
				}
			}
		}
		s.terminal, s.state, changed = event.Terminal, PlaybackStateStopped, true
		if event.Terminal == playback.TerminalError {
			s.lastError = "playback failed"
		}
		active := s.active
		active.cancel(terminalCause(event.Terminal))
		advanced := playback.ShouldAdvance(event.Terminal, s.policy.LoopSelected, s.policy.AutoPlayNext) && s.followup(active)
		if !advanced {
			s.active = nil
			s.mutation, s.cleanup, s.state = true, true, PlaybackStateStopping
			s.controller.cleanupTerminal(s.generation, active, event.Terminal)
		}
	}
	if changed {
		s.commit()
	}
}

func (s *actorState) deferMonitor(event playback.MonitorEvent) {
	if s.deferred == nil {
		deferred := event
		s.deferred = &deferred
		return
	}
	if s.deferred.Terminal == playback.TerminalFinished {
		if s.deferred.Err == nil {
			s.deferred.Err = event.Err
		}
		return
	}
	if event.Terminal == playback.TerminalFinished {
		if event.Err == nil {
			event.Err = s.deferred.Err
		}
		s.deferred = &event
		return
	}
	if s.deferred.Err == nil {
		s.deferred.Err = event.Err
	}
}

func (c *Controller) cleanupTerminal(generation uint64, active *activeSession, reason playback.TerminalReason) {
	c.goOwned(func() {
		ioCtx, cancel := context.WithTimeout(context.Background(), c.cfg.OperationTimeout)
		var err error
		if reason == playback.TerminalFinished {
			err = transitionSession(ioCtx, active, c.cfg.MediaServer, c.cfg.OperationTimeout)
		} else {
			err = teardownSession(ioCtx, active, c.cfg.MediaServer, c.cfg.OperationTimeout)
		}
		cancel()
		_ = c.enqueueInternal(message{fn: func(s *actorState) {
			if generation != s.generation || !s.cleanup {
				return
			}
			s.mutation, s.cleanup, s.state = false, false, PlaybackStateStopped
			if err != nil {
				s.lastError = "playback cleanup failed"
			}
			s.commit()
		}})
	})
}

func (s *actorState) resumeDeferredMonitor() {
	if s.deferred == nil {
		return
	}
	event := *s.deferred
	s.deferred = nil
	s.monitor(event)
}

func (s *actorState) followup(previous *activeSession) bool {
	if s.mutation {
		return false
	}
	targetCopy := previous.target
	if s.policy.LoopSelected {
		request := PlayRequest{target: &targetCopy}
		if queueIndex(s.queue, previous.itemID) >= 0 {
			request.QueueItemID = previous.itemID
		} else {
			mediaCopy := previous.media
			request.media = &mediaCopy
		}
		response := make(chan Result, 1)
		generation := s.generation
		s.beginPlay(request, response)
		return s.mutation && s.generation > generation
	}
	if s.queue == nil {
		return false
	}
	index := queueIndex(s.queue, previous.itemID)
	if index < 0 {
		return false
	}
	s.queue.SetCurrentIndex(index)
	// Desktop autoplay wraps, while manual next remains bounded. AdjacentIndex
	// excludes the current item, so singleton and only-current-type queues stop.
	target := s.queue.AdjacentIndex(1, s.policy.AutoPlaySameType, true)
	if target < 0 {
		return false
	}
	item, _ := s.queue.Item(target)
	if previous.target.AudioOnly && item.MediaKind() != mediamodel.MediaKindAudio {
		s.lastError = "device supports audio only"
		return false
	}
	response := make(chan Result, 1)
	generation := s.generation
	s.beginPlay(PlayRequest{QueueItemID: item.ID(), target: &targetCopy}, response)
	return s.mutation && s.generation > generation
}

// Refresh performs one discovery refresh and commits its snapshot.
func (c *Controller) Refresh(ctx context.Context, mutation Mutation) Result {
	if ctx == nil {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	return c.callResult(ctx, mutation.RequestID, func(s *actorState, response chan<- Result) {
		if result := s.check(mutation); !result.OK() {
			response <- result
			return
		}
		if s.refreshing {
			response <- fail(mutation.RequestID, s.revision, ErrBusy)
			return
		}
		if c.cfg.Discovery == nil {
			response <- fail(mutation.RequestID, s.revision, ErrInvalidOperation)
			return
		}
		s.refreshing = true
		c.goOwned(func() {
			ioCtx, cancel := operationContext(c.ctx, ctx, c.cfg.OperationTimeout)
			defer cancel()
			err := c.cfg.Discovery.Refresh(ioCtx)
			if enqueueErr := c.enqueueInternal(message{fn: func(s *actorState) {
				s.refreshing = false
				if err != nil {
					response <- fail(mutation.RequestID, s.revision, err)
				} else {
					s.setDevices(c.cfg.Discovery.Snapshot())
					response <- Result{RequestID: mutation.RequestID, Revision: s.revision}
				}
			}}); enqueueErr != nil {
				response <- fail(mutation.RequestID, 0, enqueueErr)
			}
		})
	})
}

var _ playback.MonitorSink = (*Controller)(nil)
var _ httphandlers.CallbackEventSink = (*Controller)(nil)
