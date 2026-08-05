//go:build !(android || ios)

package servermode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"go2tv.app/go2tv/v2/internal/managedsession"
	"go2tv.app/go2tv/v2/internal/playback"
)

// Refresh errors surfaced to the controller/WebUI use stable text only.
var (
	errManagedRefreshTimeout     = errors.New("managed discovery refresh timed out")
	errManagedRefreshUnavailable = errors.New("managed discovery refresh unavailable")
	errManagedDiscoveryClosed    = errors.New("managed discovery closed")
)

type pendingRefresh struct {
	id      string
	done    chan struct{}
	err     error
	waiters int
}

// managedDiscovery implements playback.Discovery for a GUI-managed child. It
// is fed exclusively by parent pipe frames and never scans the network.
type managedDiscovery struct {
	sendRefresh func(requestID string) error

	mu          sync.Mutex
	devices     []playback.Device
	ids         map[string]string
	revision    uint64
	haveInitial bool
	subscribers map[uint64]chan []playback.Device
	nextSub     uint64
	pending     *pendingRefresh
	nextRequest uint64
	closed      bool
	closedCh    chan struct{}
}

func newManagedDiscovery(sendRefresh func(requestID string) error) *managedDiscovery {
	return &managedDiscovery{
		sendRefresh: sendRefresh,
		ids:         make(map[string]string),
		subscribers: make(map[uint64]chan []playback.Device),
		closedCh:    make(chan struct{}),
	}
}

// Start starts no workers: the parent owns all network discovery.
func (d *managedDiscovery) Start(context.Context) {}

func (d *managedDiscovery) Snapshot() []playback.Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.devices)
}

func (d *managedDiscovery) Subscribe(buffer int) (<-chan []playback.Device, func()) {
	if buffer <= 0 {
		buffer = 1
	}
	d.mu.Lock()
	d.nextSub++
	id := d.nextSub
	ch := make(chan []playback.Device, buffer)
	d.subscribers[id] = ch
	snapshot := slices.Clone(d.devices)
	d.mu.Unlock()
	if len(snapshot) > 0 {
		ch <- snapshot
	}
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			d.mu.Lock()
			delete(d.subscribers, id)
			close(ch)
			d.mu.Unlock()
		})
	}
}

// Refresh joins the single outstanding pipe request when one is in flight,
// otherwise it sends a new one. All concurrent callers share one result. A
// caller leaving on context timeout removes only its waiter; the last waiter
// leaving retires the request so a late result is dropped, not matched.
func (d *managedDiscovery) Refresh(ctx context.Context) error {
	if ctx == nil {
		return errors.New("managed discovery: context required")
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return errManagedDiscoveryClosed
	}
	pending := d.pending
	if pending == nil {
		d.nextRequest++
		pending = &pendingRefresh{id: strconv.FormatUint(d.nextRequest, 10), done: make(chan struct{}), waiters: 1}
		d.pending = pending
		send := d.sendRefresh
		d.mu.Unlock()
		if err := send(pending.id); err != nil {
			d.failPending(pending, errManagedRefreshUnavailable)
		}
	} else {
		pending.waiters++
		d.mu.Unlock()
	}

	select {
	case <-pending.done:
		return pending.err
	case <-d.closedCh:
		return errManagedDiscoveryClosed
	case <-ctx.Done():
		d.mu.Lock()
		pending.waiters--
		if d.pending == pending && pending.waiters <= 0 {
			d.pending = nil
		}
		d.mu.Unlock()
		return fmt.Errorf("%w: %w", errManagedRefreshTimeout, ctx.Err())
	}
}

// ApplySnapshot ingests one pushed parent snapshot; stale revisions are ignored.
func (d *managedDiscovery) ApplySnapshot(revision uint64, devices []managedsession.Device) {
	d.mu.Lock()
	if d.closed || (d.haveInitial && revision <= d.revision) {
		d.mu.Unlock()
		return
	}
	d.applyLocked(revision, devices)
	d.mu.Unlock()
}

// ApplyRefreshResult resolves the outstanding refresh request. Late or
// mismatched results are dropped after applying any newer snapshot they carry.
func (d *managedDiscovery) ApplyRefreshResult(requestID string, revision uint64, devices []managedsession.Device, errorCode string) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	if errorCode == "" && (!d.haveInitial || revision > d.revision) {
		d.applyLocked(revision, devices)
	}
	pending := d.pending
	if pending == nil || pending.id != requestID {
		d.mu.Unlock()
		return
	}
	d.pending = nil
	d.mu.Unlock()

	switch errorCode {
	case "":
		pending.err = nil
	case managedsession.RefreshErrorTimeout:
		pending.err = errManagedRefreshTimeout
	default:
		pending.err = errManagedRefreshUnavailable
	}
	close(pending.done)
}

// applyLocked maps wire devices onto stable per-child opaque IDs and performs
// a latest-only publish. Callers hold d.mu.
func (d *managedDiscovery) applyLocked(revision uint64, devices []managedsession.Device) {
	next := make([]playback.Device, 0, len(devices))
	for _, device := range devices {
		key := device.Protocol + "\x00" + device.Endpoint
		id := d.ids[key]
		if id == "" {
			id = randomOpaqueID()
			d.ids[key] = id
		}
		next = append(next, playback.Device{
			ID: id, Name: device.Name, Protocol: device.Protocol,
			AudioOnly: device.AudioOnly, Endpoint: device.Endpoint,
		})
	}
	d.devices = next
	d.revision = revision
	d.haveInitial = true
	for _, subscriber := range d.subscribers {
		update := slices.Clone(next)
		select {
		case subscriber <- update:
		default:
			select {
			case <-subscriber:
			default:
			}
			select {
			case subscriber <- update:
			default:
			}
		}
	}
}

// Close unblocks pending refresh waiters and stops accepting updates.
func (d *managedDiscovery) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	pending := d.pending
	d.pending = nil
	close(d.closedCh)
	d.mu.Unlock()
	if pending != nil {
		pending.err = errManagedDiscoveryClosed
		close(pending.done)
	}
}

func (d *managedDiscovery) failPending(pending *pendingRefresh, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending != pending {
		return
	}
	d.pending = nil
	pending.err = err
	close(pending.done)
}

func randomOpaqueID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("managed discovery: random source failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}
