package devices

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// discoveryRefreshBound caps how long RefreshDiscovery waits for the existing
// discovery loops to complete their next scans. The DLNA loop completes a scan
// roughly every 3s and the Chromecast workers poll every 1s/4s, so this bound
// only fires when the loops are not running or the network is wedged.
const discoveryRefreshBound = 10 * time.Second

// ErrDiscoveryRefreshTimeout reports that the existing discovery loops did not
// complete a scan within the refresh bound.
var ErrDiscoveryRefreshTimeout = errors.New("discovery refresh timed out")

var feed = newDiscoveryFeed(SnapshotAllDevices)

type refreshOp struct {
	done chan struct{}
	err  error
}

// discoveryFeed publishes cache snapshots and coordinates coalesced refresh
// waits on top of the existing discovery loops. It never scans the network.
type discoveryFeed struct {
	snapshot    func() []Device
	mu          sync.Mutex
	subscribers map[uint64]chan []Device
	nextSub     uint64
	last        []Device
	published   bool
	dlnaScan    chan struct{}
	ccScan      chan struct{}
	inflight    *refreshOp
}

func newDiscoveryFeed(snapshot func() []Device) *discoveryFeed {
	return &discoveryFeed{
		snapshot:    snapshot,
		subscribers: make(map[uint64]chan []Device),
		dlnaScan:    make(chan struct{}),
		ccScan:      make(chan struct{}),
	}
}

// SnapshotAllDevices returns the current cached DLNA and Chromecast devices.
// An empty result is valid; no network scan is performed.
func SnapshotAllDevices() []Device {
	combined := append(getDLNADevices(), getChromecastDevicesSnapshot()...)
	sortDevices(combined)
	return combined
}

// SubscribeDiscovery delivers the current snapshot immediately and a new
// snapshot after every cache change, including changes to empty. Publishing is
// latest-only: slow consumers observe the newest snapshot, not every one.
func SubscribeDiscovery(buffer int) (<-chan []Device, func()) {
	return feed.subscribe(buffer)
}

// RefreshDiscovery asks the existing discovery loops for fresh results and
// waits boundedly for the next completed DLNA and Chromecast scans.
// Overlapping calls share one wait; no parallel scan plane is created.
func RefreshDiscovery(ctx context.Context) error {
	return feed.refresh(ctx)
}

func (f *discoveryFeed) subscribe(buffer int) (<-chan []Device, func()) {
	if buffer <= 0 {
		buffer = 1
	}
	f.mu.Lock()
	f.nextSub++
	id := f.nextSub
	ch := make(chan []Device, buffer)
	f.subscribers[id] = ch
	snapshot := f.snapshot()
	f.mu.Unlock()
	ch <- snapshot
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			f.mu.Lock()
			delete(f.subscribers, id)
			close(ch)
			f.mu.Unlock()
		})
	}
	return ch, cancel
}

// publish recomputes the combined snapshot and notifies subscribers when it
// changed. Callers must not hold dlnaMu or ccMu.
func (f *discoveryFeed) publish() {
	f.mu.Lock()
	defer f.mu.Unlock()
	snapshot := f.snapshot()
	if f.published && slices.Equal(snapshot, f.last) {
		return
	}
	f.last = snapshot
	f.published = true
	for _, subscriber := range f.subscribers {
		update := slices.Clone(snapshot)
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

func (f *discoveryFeed) notifyDLNAScan() {
	f.mu.Lock()
	close(f.dlnaScan)
	f.dlnaScan = make(chan struct{})
	f.mu.Unlock()
}

func (f *discoveryFeed) notifyChromecastScan() {
	f.mu.Lock()
	close(f.ccScan)
	f.ccScan = make(chan struct{})
	f.mu.Unlock()
}

func (f *discoveryFeed) refresh(ctx context.Context) error {
	if ctx == nil {
		return errors.New("RefreshDiscovery: context required")
	}
	f.mu.Lock()
	op := f.inflight
	if op == nil {
		op = &refreshOp{done: make(chan struct{})}
		f.inflight = op
		dlna := f.dlnaScan
		cc := f.ccScan
		go f.awaitScans(op, dlna, cc)
	}
	f.mu.Unlock()
	select {
	case <-op.done:
		return op.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *discoveryFeed) awaitScans(op *refreshOp, dlna, cc <-chan struct{}) {
	bound := time.NewTimer(discoveryRefreshBound)
	defer bound.Stop()
	var err error
	for dlna != nil || cc != nil {
		select {
		case <-dlna:
			dlna = nil
		case <-cc:
			cc = nil
		case <-bound.C:
			err = ErrDiscoveryRefreshTimeout
			dlna, cc = nil, nil
		}
	}
	f.mu.Lock()
	f.inflight = nil
	f.mu.Unlock()
	op.err = err
	close(op.done)
}
