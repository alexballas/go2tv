package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/metadata"
)

type message struct {
	fn   func(*actorState)
	done chan struct{}
}

type activeSession struct {
	generation uint64
	target     playback.Device
	itemID     string
	media      MediaRef
	subtitle   SubtitleRef
	kind       mediamodel.MediaKind
	transport  Transport
	server     playback.ServerRequest
	load       playback.LoadRequest
	ctx        context.Context
	cancel     context.CancelCauseFunc
}

func terminalCause(reason playback.TerminalReason) error { return errors.New(string(reason)) }

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
}

type Controller struct {
	cfg       Config
	ctx       context.Context
	cancel    context.CancelCauseFunc
	queue     chan message
	callbacks chan playback.MonitorEvent
	done      chan struct{}
	closeOnce sync.Once
}

func New(cfg Config) *Controller {
	if cfg.OperationTimeout <= 0 {
		cfg.OperationTimeout = 30 * time.Second
	}
	if cfg.Artwork == nil {
		cfg.Artwork = NewArtworkCache(ArtworkCacheBytes)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	c := &Controller{cfg: cfg, ctx: ctx, cancel: cancel, queue: make(chan message, ActorQueueSize), callbacks: make(chan playback.MonitorEvent, CallbackQueueSize), done: make(chan struct{})}
	go c.run()
	go c.forwardCallbacks()
	if cfg.Discovery != nil {
		updates, unsubscribe := cfg.Discovery.Subscribe(1)
		cfg.Discovery.Start(ctx)
		go func() {
			defer unsubscribe()
			for {
				select {
				case <-ctx.Done():
					return
				case devices, ok := <-updates:
					if !ok {
						return
					}
					_ = c.enqueue(message{fn: func(s *actorState) { s.setDevices(devices) }})
				}
			}
		}()
	}
	return c
}

func (c *Controller) run() {
	defer close(c.done)
	s := &actorState{controller: c, policy: DefaultPolicy(), state: "STOPPED", volume: 100}
	for {
		select {
		case <-c.ctx.Done():
			if s.active != nil {
				s.active.cancel(terminalCause(playback.TerminalShutdown))
				ctx, cancel := context.WithTimeout(context.Background(), c.cfg.OperationTimeout)
				_ = teardown(ctx, s.active.transport, c.cfg.MediaServer)
				cancel()
			}
			return
		case msg := <-c.queue:
			msg.fn(s)
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
			_ = c.enqueue(message{fn: func(s *actorState) { s.monitor(event) }})
		}
	}
}

func (c *Controller) Close() {
	c.closeOnce.Do(func() { c.cancel(terminalCause(playback.TerminalShutdown)); <-c.done })
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

func (c *Controller) Snapshot(ctx context.Context) (Snapshot, error) {
	if ctx == nil {
		return Snapshot{}, ErrInvalidOperation
	}
	var snapshot Snapshot
	done := make(chan struct{})
	err := c.enqueue(message{done: done, fn: func(s *actorState) { snapshot = s.snapshot() }})
	if err != nil {
		return Snapshot{}, err
	}
	select {
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-done:
		return snapshot, nil
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
	code, message := CodeInternal, "operation failed"
	switch {
	case errors.Is(err, ErrBusy):
		code, message = CodeBusy, "operation busy"
	case errors.Is(err, ErrConflict):
		code, message = CodeConflict, "state changed"
	case errors.Is(err, ErrNotFound):
		code, message = CodeNotFound, "not found"
	case errors.Is(err, ErrNoDevice):
		code, message = CodeNoDevice, "select a device"
	case errors.Is(err, ErrNoMedia):
		code, message = CodeNoMedia, "select media"
	case errors.Is(err, ErrNoSession):
		code, message = CodeNoSession, "nothing playing"
	case errors.Is(err, ErrAudioOnly):
		code, message = CodeAudioOnly, "device supports audio only"
	case errors.Is(err, ErrQueueLimit):
		code, message = CodeQueueLimit, "queue too large"
	case errors.Is(err, ErrInvalidPolicy), errors.Is(err, ErrInvalidOperation):
		code, message = CodeInvalid, "invalid request"
	case errors.Is(err, ErrSeekUnsupported), errors.Is(err, playback.ErrSeekUnsupported):
		code, message = CodeInvalid, "seek unsupported"
	case errors.Is(err, playback.ErrSeekNegative), errors.Is(err, playback.ErrSeekPastDuration):
		code, message = CodeInvalid, "invalid seek position"
	case errors.Is(err, ErrClosed):
		code, message = CodeClosed, "controller closed"
	}
	return Result{RequestID: requestID, Revision: revision, Code: code, Message: message}
}

func (c *Controller) mutate(ctx context.Context, mutation Mutation, fn func(*actorState) Result) Result {
	if ctx == nil {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	result := Result{RequestID: mutation.RequestID}
	done := make(chan struct{})
	if err := c.enqueue(message{done: done, fn: func(s *actorState) { result = fn(s) }}); err != nil {
		return fail(mutation.RequestID, 0, err)
	}
	select {
	case <-ctx.Done():
		return fail(mutation.RequestID, result.Revision, ctx.Err())
	case <-done:
		return result
	}
}

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

func (c *Controller) SelectMedia(ctx context.Context, mutation Mutation, media MediaRef) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		if !media.valid() {
			return fail(mutation.RequestID, s.revision, ErrInvalidOperation)
		}
		s.media = media
		s.mediaQueueID = ""
		s.artworkID = mediaArtworkID(media)
		s.commit()
		return Result{RequestID: mutation.RequestID, Revision: s.revision}
	})
}

func (c *Controller) SelectSubtitle(ctx context.Context, mutation Mutation, subtitle SubtitleRef) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		if subtitle.ID != "" && !subtitle.valid() {
			return fail(mutation.RequestID, s.revision, ErrInvalidOperation)
		}
		s.subtitle = subtitle
		s.commit()
		return Result{RequestID: mutation.RequestID, Revision: s.revision}
	})
}

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

func (c *Controller) AddQueueItem(ctx context.Context, request QueueAddRequest) QueueAddResult {
	var itemID string
	result := c.mutate(ctx, request.Mutation, func(s *actorState) Result {
		if result := s.check(request.Mutation); !result.OK() {
			return result
		}
		if !request.Media.valid() {
			return fail(request.RequestID, s.revision, ErrInvalidOperation)
		}
		if s.queue != nil && s.queue.Len() >= MaxQueueItems {
			return fail(request.RequestID, s.revision, ErrQueueLimit)
		}
		item, ok := mediamodel.NewQueueReference(request.Media.ID, request.Media.Name, request.Media.Parent, request.Media.Kind)
		if !ok {
			return fail(request.RequestID, s.revision, ErrInvalidOperation)
		}
		items, current := []mediamodel.QueueItem{item}, -1
		if s.queue != nil {
			items = append(s.queue.Items(), item)
			current = s.queue.CurrentIndex()
		}
		s.queue = mediamodel.NewQueue(items, current)
		if s.queueRefs == nil {
			s.queueRefs = make(map[string]MediaRef)
		}
		s.queueRefs[item.ID()] = request.Media
		itemID = item.ID()
		s.commit()
		return Result{RequestID: request.RequestID, Revision: s.revision}
	})
	return QueueAddResult{Result: result, ItemID: itemID}
}

func (c *Controller) ClearQueue(ctx context.Context, mutation Mutation) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		s.queue = nil
		s.queueRefs = nil
		if s.mediaQueueID != "" {
			s.media, s.mediaQueueID = MediaRef{}, ""
			s.artworkID = ""
		}
		s.commit()
		return Result{RequestID: mutation.RequestID, Revision: s.revision}
	})
}

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

func (c *Controller) RemoveQueueItem(ctx context.Context, mutation Mutation, id string) Result {
	return c.mutate(ctx, mutation, func(s *actorState) Result {
		if result := s.check(mutation); !result.OK() {
			return result
		}
		index := queueIndex(s.queue, id)
		if index < 0 {
			return fail(mutation.RequestID, s.revision, ErrNotFound)
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

func (c *Controller) SetPolicy(ctx context.Context, request PolicyRequest) Result {
	return c.mutate(ctx, request.Mutation, func(s *actorState) Result {
		if result := s.check(request.Mutation); !result.OK() {
			return result
		}
		p := request.Policy
		if p.ImageDurationSeconds < 0 || p.LoopSelected && p.AutoPlayNext || !p.AutoPlayNext && (p.AutoPlaySameType || p.GaplessEnabled) {
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
		s.policy = p
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

func mediaMIME(kind mediamodel.MediaKind) string {
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
	if request.QueueItemID == "" && s.media.valid() {
		media, ok = s.media, true
	}
	if !ok {
		return target, item, MediaRef{}, ErrNoMedia
	}
	return target, item, media, nil
}

func (c *Controller) Play(ctx context.Context, request PlayRequest) Result {
	if ctx == nil {
		return fail(request.RequestID, 0, ErrInvalidOperation)
	}
	response := make(chan Result, 1)
	if err := c.enqueue(message{fn: func(s *actorState) { s.beginPlay(request, response) }}); err != nil {
		return fail(request.RequestID, 0, err)
	}
	select {
	case <-ctx.Done():
		return fail(request.RequestID, 0, ctx.Err())
	case result := <-response:
		return result
	}
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
	s.mutation = true
	s.state = "LOADING"
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
	go s.controller.playIO(opCtx, generation, target, item, media, old, cancel, request, response, s.transcode, s.subtitle)
}

type playCompletion struct {
	generation uint64
	session    *activeSession
	err        error
	request    PlayRequest
	response   chan<- Result
}

type existingLoader interface {
	LoadOnExisting(context.Context, playback.LoadRequest) error
}

func (c *Controller) playIO(ctx context.Context, generation uint64, target playback.Device, item mediamodel.QueueItem, media MediaRef, old *activeSession, cancel context.CancelCauseFunc, request PlayRequest, response chan<- Result, transcode bool, subtitle SubtitleRef) {
	ioCtx, timeoutCancel := context.WithTimeout(ctx, c.cfg.OperationTimeout)
	defer timeoutCancel()
	var reusedCast existingLoader
	if old != nil {
		if target.Protocol == "Chromecast" && old.target.ID == target.ID {
			reusedCast, _ = old.transport.(existingLoader)
		}
		var err error
		if reusedCast != nil {
			err = c.cfg.MediaServer.Stop(ioCtx)
		} else {
			err = teardown(ioCtx, old.transport, c.cfg.MediaServer)
		}
		if err != nil {
			c.completePlay(playCompletion{generation: generation, err: err, request: request, response: response})
			return
		}
	}
	if err := ioCtx.Err(); err != nil {
		c.completePlay(playCompletion{generation: generation, err: err, request: request, response: response})
		return
	}
	var transport Transport
	var err error
	if reusedCast != nil {
		transport = old.transport
	} else {
		transport, err = c.cfg.TransportFactory.Open(ioCtx, target)
		if err != nil {
			c.completePlay(playCompletion{generation: generation, err: err, request: request, response: response})
			return
		}
	}
	opener := media.OpenDirect
	if transcode {
		opener = media.OpenTranscode
		if opener == nil {
			_ = teardown(context.Background(), transport, c.cfg.MediaServer)
			c.completePlay(playCompletion{generation: generation, err: ErrInvalidOperation, request: request, response: response})
			return
		}
	}
	serverRequest := playback.ServerRequest{Media: opener, MediaExt: media.extension(), MediaType: mediaMIME(item.MediaKind()), Transcode: transcode, Target: target}
	if subtitle.valid() {
		serverRequest.Subtitle, serverRequest.SubtitleExt = subtitle.Open, subtitle.extension()
	}
	route, err := c.cfg.MediaServer.Start(ioCtx, serverRequest)
	loadMediaType := mediaMIME(item.MediaKind())
	if transcode && target.Protocol == "Chromecast" {
		loadMediaType = "video/mp4"
	}
	loadRequest := playback.LoadRequest{MediaURL: route.URL, MediaType: loadMediaType, SubtitleURL: route.SubtitleURL, Seekable: !transcode, Metadata: metadata.Media{Title: item.BaseName()}}
	if media.Artwork != nil {
		loadRequest.ArtworkData = append([]byte(nil), media.Artwork.Data...)
		artRoute, artErr := c.cfg.MediaServer.Add(ioCtx, playback.RouteRequest{MediaType: media.Artwork.MIMEType, Contents: loadRequest.ArtworkData})
		if artErr == nil {
			loadRequest.Metadata.Artwork = &metadata.Artwork{URL: artRoute.URL, MIMEType: media.Artwork.MIMEType, Width: media.Artwork.Width, Height: media.Artwork.Height}
		}
	}
	if err == nil && reusedCast != nil {
		err = reusedCast.LoadOnExisting(ioCtx, loadRequest)
	} else if err == nil {
		err = transport.Load(ioCtx, loadRequest)
	}
	// Chromecast LOAD autoplays. Calling Play before its media session status
	// arrives fails with "media not yet initialised" and tears down a good load.
	if err == nil && target.Protocol != "Chromecast" {
		err = transport.Play(ioCtx)
	}
	if err != nil {
		_ = teardown(context.Background(), transport, c.cfg.MediaServer)
		c.completePlay(playCompletion{generation: generation, err: err, request: request, response: response})
		return
	}
	c.completePlay(playCompletion{generation: generation, session: &activeSession{generation: generation, target: target, itemID: item.ID(), media: media, subtitle: subtitle, kind: item.MediaKind(), transport: transport, server: serverRequest, load: loadRequest, ctx: ctx, cancel: cancel}, request: request, response: response})
}

func teardown(ctx context.Context, transport Transport, server playback.MediaServer) error {
	var result error
	if transport != nil {
		result = errors.Join(result, transport.Stop(ctx))
	}
	if server != nil {
		result = errors.Join(result, server.Stop(ctx))
	}
	if transport != nil {
		result = errors.Join(result, transport.Close(ctx))
	}
	return result
}

func (c *Controller) completePlay(completion playCompletion) {
	if err := c.enqueue(message{fn: func(s *actorState) {
		if completion.generation != s.generation {
			if completion.session != nil {
				completion.session.cancel(terminalCause(playback.TerminalReplacement))
				go teardown(context.Background(), completion.session.transport, c.cfg.MediaServer)
			}
			completion.response <- fail(completion.request.RequestID, s.revision, ErrBusy)
			return
		}
		s.mutation = false
		if completion.err != nil {
			s.active, s.state, s.lastError, s.terminal = nil, "STOPPED", "playback failed", playback.TerminalError
			s.commit()
			completion.response <- fail(completion.request.RequestID, s.revision, completion.err)
			return
		}
		s.active, s.state, s.position, s.duration, s.lastError, s.terminal = completion.session, "PLAYING", 0, 0, "", ""
		s.commit()
		if completion.session.kind == mediamodel.MediaKindImage && s.policy.ImageDurationSeconds > 0 {
			session := completion.session
			delay := time.Duration(s.policy.ImageDurationSeconds) * time.Second
			go func() {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-session.ctx.Done():
				case <-timer.C:
					c.HandleMonitorEvent(session.ctx, playback.MonitorEvent{Generation: session.generation, Terminal: playback.TerminalFinished})
				}
			}()
		} else if c.cfg.RunMonitor != nil {
			session := completion.session
			go c.cfg.RunMonitor(session.ctx, session.generation, session.target, session.transport, c)
		}
		completion.response <- Result{RequestID: completion.request.RequestID, Revision: s.revision}
	}}); err != nil {
		if completion.session != nil {
			completion.session.cancel(terminalCause(playback.TerminalShutdown))
			_ = teardown(context.Background(), completion.session.transport, c.cfg.MediaServer)
		}
		completion.response <- fail(completion.request.RequestID, 0, err)
	}
}

func (c *Controller) Stop(ctx context.Context, mutation Mutation) Result {
	if ctx == nil {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	response := make(chan Result, 1)
	if err := c.enqueue(message{fn: func(s *actorState) {
		if result := s.check(mutation); !result.OK() {
			response <- result
			return
		}
		s.generation++
		s.terminal, s.state = playback.TerminalUserStop, "STOPPING"
		active := s.active
		if active != nil {
			active.cancel(terminalCause(playback.TerminalUserStop))
		}
		s.active, s.mutation = nil, true
		s.commit()
		if active == nil {
			s.mutation, s.state = false, "STOPPED"
			response <- Result{RequestID: mutation.RequestID, Revision: s.revision}
			return
		}
		go func(generation uint64) {
			ioCtx, cancel := context.WithTimeout(context.Background(), c.cfg.OperationTimeout)
			defer cancel()
			err := teardown(ioCtx, active.transport, c.cfg.MediaServer)
			_ = c.enqueue(message{fn: func(s *actorState) {
				if generation == s.generation {
					s.mutation, s.state = false, "STOPPED"
					s.commit()
					if err != nil {
						s.lastError = "stop failed"
						response <- fail(mutation.RequestID, s.revision, err)
					} else {
						response <- Result{RequestID: mutation.RequestID, Revision: s.revision}
					}
				}
			}})
		}(s.generation)
	}}); err != nil {
		return fail(mutation.RequestID, 0, err)
	}
	select {
	case <-ctx.Done():
		return fail(mutation.RequestID, 0, ctx.Err())
	case result := <-response:
		return result
	}
}

func (c *Controller) Pause(ctx context.Context, mutation Mutation) Result {
	return c.transportControl(ctx, mutation, "paused", func(t Transport) error { return t.Pause(ctx) })
}
func (c *Controller) Resume(ctx context.Context, mutation Mutation) Result {
	return c.transportControl(ctx, mutation, "play", func(t Transport) error { return t.Play(ctx) })
}

func (c *Controller) Seek(ctx context.Context, request SeekRequest) Result {
	if ctx == nil {
		return fail(request.RequestID, 0, ErrInvalidOperation)
	}
	response := make(chan Result, 1)
	if err := c.enqueue(message{fn: func(s *actorState) {
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
		generation := s.generation
		sessionCtx, sessionCancel := context.WithCancelCause(c.ctx)
		active.ctx, active.cancel, active.generation = sessionCtx, sessionCancel, generation
		duration := s.duration
		go func() {
			ioCtx, cancel := context.WithTimeout(ctx, c.cfg.OperationTimeout)
			defer cancel()
			engine := playback.NewSeekEngine(dlna, cast, c.cfg.MediaServer)
			_, err := engine.Seek(ioCtx, playback.SeekRequest{Protocol: active.target.Protocol, Transcoded: active.server.Transcode, Seconds: request.Seconds, Duration: duration, Server: active.server, Load: active.load})
			if enqueueErr := c.enqueue(message{fn: func(s *actorState) {
				if s.active != active {
					response <- fail(request.RequestID, s.revision, ErrBusy)
					return
				}
				s.mutation = false
				if err != nil {
					s.lastError = "seek failed"
					if c.cfg.RunMonitor != nil {
						go c.cfg.RunMonitor(sessionCtx, generation, active.target, active.transport, c)
					}
					response <- fail(request.RequestID, s.revision, err)
					return
				}
				s.position, s.state = request.Seconds, "PLAYING"
				s.commit()
				if c.cfg.RunMonitor != nil {
					go c.cfg.RunMonitor(sessionCtx, generation, active.target, active.transport, c)
				}
				response <- Result{RequestID: request.RequestID, Revision: s.revision}
			}}); enqueueErr != nil {
				response <- fail(request.RequestID, 0, enqueueErr)
			}
		}()
	}}); err != nil {
		return fail(request.RequestID, 0, err)
	}
	select {
	case <-ctx.Done():
		return fail(request.RequestID, 0, ctx.Err())
	case result := <-response:
		return result
	}
}

func (c *Controller) transportControl(ctx context.Context, mutation Mutation, state string, call func(Transport) error) Result {
	if ctx == nil {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	response := make(chan Result, 1)
	if err := c.enqueue(message{fn: func(s *actorState) {
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
		go func() {
			err := call(transport)
			_ = c.enqueue(message{fn: func(s *actorState) {
				if generation != s.generation {
					response <- fail(mutation.RequestID, s.revision, ErrBusy)
					return
				}
				s.mutation = false
				if err != nil {
					s.lastError = "control failed"
					response <- fail(mutation.RequestID, s.revision, err)
					return
				}
				s.state = strings.ToUpper(state)
				s.commit()
				response <- Result{RequestID: mutation.RequestID, Revision: s.revision}
			}})
		}()
	}}); err != nil {
		return fail(mutation.RequestID, 0, err)
	}
	select {
	case <-ctx.Done():
		return fail(mutation.RequestID, 0, ctx.Err())
	case result := <-response:
		return result
	}
}

func (c *Controller) SetVolume(ctx context.Context, mutation Mutation, volume int) Result {
	if volume < 0 || volume > 100 {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	return c.deviceControl(ctx, mutation, func(t Transport) error { return t.SetVolume(ctx, volume) }, func(s *actorState) { s.volume = volume })
}

func (c *Controller) AdjustVolume(ctx context.Context, mutation Mutation, delta int) Result {
	if delta != -1 && delta != 1 {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	volume := 0
	return c.deviceControl(ctx, mutation, func(t Transport) error {
		current, err := t.Volume(ctx)
		if err != nil {
			return err
		}
		volume = max(0, min(100, current+delta))
		return t.SetVolume(ctx, volume)
	}, func(s *actorState) { s.volume = volume })
}

func (c *Controller) SetMute(ctx context.Context, mutation Mutation, muted bool) Result {
	return c.deviceControl(ctx, mutation, func(t Transport) error { return t.SetMute(ctx, muted) }, func(s *actorState) { s.muted = muted })
}

func (c *Controller) deviceControl(ctx context.Context, mutation Mutation, call func(Transport) error, commit func(*actorState)) Result {
	if ctx == nil {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	response := make(chan Result, 1)
	if err := c.enqueue(message{fn: func(s *actorState) {
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
		} else {
			device, ok := s.selectedDevice()
			if !ok {
				response <- fail(mutation.RequestID, s.revision, ErrNoDevice)
				return
			}
			target, transient = device, true
		}
		s.mutation = true
		generation := s.generation
		go func() {
			var err error
			if transient {
				transport, err = c.cfg.TransportFactory.Open(ctx, target)
			}
			if err == nil {
				err = call(transport)
			}
			if transient {
				if transport != nil {
					err = errors.Join(err, transport.Close(ctx))
				}
			}
			if enqueueErr := c.enqueue(message{fn: func(s *actorState) {
				if generation != s.generation {
					response <- fail(mutation.RequestID, s.revision, ErrBusy)
					return
				}
				s.mutation = false
				if err != nil {
					response <- fail(mutation.RequestID, s.revision, err)
					return
				}
				commit(s)
				s.commit()
				response <- Result{RequestID: mutation.RequestID, Revision: s.revision}
			}}); enqueueErr != nil {
				response <- fail(mutation.RequestID, 0, enqueueErr)
			}
		}()
	}}); err != nil {
		return fail(mutation.RequestID, 0, err)
	}
	select {
	case <-ctx.Done():
		return fail(mutation.RequestID, 0, ctx.Err())
	case result := <-response:
		return result
	}
}

func (c *Controller) HandleMonitorEvent(_ context.Context, event playback.MonitorEvent) {
	select {
	case <-c.ctx.Done():
		return
	case c.callbacks <- event:
	default:
	}
}

func (c *Controller) HandleCallbackEvent(_ context.Context, event httphandlers.CallbackEvent) {
	monitor := playback.MonitorEvent{Generation: event.Generation}
	switch event.TransportState {
	case "PLAYING":
		monitor.State = "PLAYING"
	case "PAUSED_PLAYBACK":
		monitor.State = "PAUSED"
	case "STOPPED":
		monitor.Terminal = playback.TerminalFinished
	default:
		return
	}
	c.HandleMonitorEvent(context.Background(), monitor)
}

func (s *actorState) monitor(event playback.MonitorEvent) {
	if s.active == nil || event.Generation != s.active.generation {
		return
	}
	changed := false
	if event.State != "" {
		s.state, changed = event.State, true
	}
	if event.Position != 0 || event.Duration != 0 {
		s.position, s.duration, changed = event.Position, event.Duration, true
	}
	if event.Terminal != "" {
		s.terminal, s.state, changed = event.Terminal, "STOPPED", true
		if event.Err != nil {
			s.lastError = "playback failed"
		}
		active := s.active
		active.cancel(terminalCause(event.Terminal))
		advanced := playback.ShouldAdvance(event.Terminal, s.policy.LoopSelected, s.policy.AutoPlayNext) && s.followup(active)
		if !advanced {
			s.active = nil
			go teardown(context.Background(), active.transport, s.controller.cfg.MediaServer)
		}
	}
	if changed {
		s.commit()
	}
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
		s.beginPlay(request, response)
		go func() { <-response }()
		return true
	}
	if s.queue == nil {
		return false
	}
	index := queueIndex(s.queue, previous.itemID)
	if index < 0 {
		return false
	}
	s.queue.SetCurrentIndex(index)
	target := s.queue.AdjacentIndex(1, s.policy.AutoPlaySameType, false)
	if target < 0 {
		return false
	}
	item, _ := s.queue.Item(target)
	if previous.target.AudioOnly && item.MediaKind() != mediamodel.MediaKindAudio {
		s.lastError = "device supports audio only"
		return false
	}
	response := make(chan Result, 1)
	s.beginPlay(PlayRequest{QueueItemID: item.ID(), target: &targetCopy}, response)
	go func() { <-response }()
	return true
}

func (c *Controller) Refresh(ctx context.Context, mutation Mutation) Result {
	if ctx == nil {
		return fail(mutation.RequestID, 0, ErrInvalidOperation)
	}
	response := make(chan Result, 1)
	if err := c.enqueue(message{fn: func(s *actorState) {
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
		go func() {
			err := c.cfg.Discovery.Refresh(ctx)
			if enqueueErr := c.enqueue(message{fn: func(s *actorState) {
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
		}()
	}}); err != nil {
		return fail(mutation.RequestID, 0, err)
	}
	select {
	case <-ctx.Done():
		return fail(mutation.RequestID, 0, ctx.Err())
	case result := <-response:
		return result
	}
}

var _ playback.MonitorSink = (*Controller)(nil)
var _ httphandlers.CallbackEventSink = (*Controller)(nil)

func (r Result) Error() error {
	if r.OK() {
		return nil
	}
	return fmt.Errorf("%s: %s", r.Code, r.Message)
}
