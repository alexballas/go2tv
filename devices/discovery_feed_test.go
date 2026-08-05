package devices

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/mdns"
)

func receiveSnapshot(t *testing.T, ch <-chan []Device) []Device {
	t.Helper()
	select {
	case snapshot := <-ch:
		return snapshot
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for discovery snapshot")
		return nil
	}
}

func snapshotHasName(snapshot []Device, name string) bool {
	for _, device := range snapshot {
		if device.Name == name {
			return true
		}
	}
	return false
}

func awaitSnapshotWithName(t *testing.T, ch <-chan []Device, name string, want bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case snapshot := <-ch:
			if snapshotHasName(snapshot, name) == want {
				return
			}
		case <-deadline:
			t.Fatalf("never observed snapshot with %q presence=%v", name, want)
		}
	}
}

func TestSubscribeDiscoveryPublishesInitialSnapshot(t *testing.T) {
	t.Parallel()
	f := newDiscoveryFeed(func() []Device { return nil })
	ch, cancel := f.subscribe(1)
	defer cancel()
	if snapshot := receiveSnapshot(t, ch); len(snapshot) != 0 {
		t.Fatalf("initial snapshot = %v, want empty", snapshot)
	}
}

func TestDiscoveryFeedPublishesChangesIncludingEmpty(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var current []Device
	f := newDiscoveryFeed(func() []Device {
		mu.Lock()
		defer mu.Unlock()
		return append([]Device(nil), current...)
	})
	ch, cancel := f.subscribe(1)
	defer cancel()
	receiveSnapshot(t, ch)

	mu.Lock()
	current = []Device{{Name: "TV", Addr: "http://192.168.1.20:1400/device.xml", Type: DeviceTypeDLNA}}
	mu.Unlock()
	f.publish()
	if snapshot := receiveSnapshot(t, ch); len(snapshot) != 1 || snapshot[0].Name != "TV" {
		t.Fatalf("snapshot = %v, want one TV", snapshot)
	}

	mu.Lock()
	current = nil
	mu.Unlock()
	f.publish()
	if snapshot := receiveSnapshot(t, ch); len(snapshot) != 0 {
		t.Fatalf("snapshot after removal = %v, want empty", snapshot)
	}
}

func TestDiscoveryFeedSkipsUnchangedSnapshots(t *testing.T) {
	t.Parallel()
	devicesList := []Device{{Name: "TV", Addr: "http://192.168.1.20:1400/device.xml", Type: DeviceTypeDLNA}}
	f := newDiscoveryFeed(func() []Device { return append([]Device(nil), devicesList...) })
	ch, cancel := f.subscribe(2)
	defer cancel()
	receiveSnapshot(t, ch)

	f.publish()
	f.publish()
	select {
	case snapshot := <-ch:
		if len(snapshot) != 1 || snapshot[0].Name != "TV" {
			t.Fatalf("unexpected snapshot: %v", snapshot)
		}
		// The first publish after subscribe may legitimately deliver once when
		// the subscriber snapshot predates publish-side change tracking; a
		// second identical publish must not deliver again.
		select {
		case extra := <-ch:
			t.Fatalf("unexpected duplicate snapshot: %v", extra)
		case <-time.After(100 * time.Millisecond):
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDiscoveryFeedWiringDLNACacheSwap(t *testing.T) {
	const planted = "Feed Wiring DLNA Device"
	ch, cancel := SubscribeDiscovery(4)
	defer cancel()
	receiveSnapshot(t, ch)

	setDLNADevices([]Device{{Name: planted, Addr: "http://192.0.2.10:1400/device.xml", Type: DeviceTypeDLNA}})
	awaitSnapshotWithName(t, ch, planted, true)

	setDLNADevices(nil)
	awaitSnapshotWithName(t, ch, planted, false)
}

func TestDiscoveryFeedWiringChromecastUpsertAndEviction(t *testing.T) {
	const planted = "Feed Wiring Chromecast"

	ch, cancel := SubscribeDiscovery(4)
	defer cancel()
	receiveSnapshot(t, ch)

	upsertChromecastFromMDNSEntry(&mdns.ServiceEntry{
		Name:       planted + "._googlecast._tcp.local.",
		AddrV4:     []byte{192, 0, 2, 30},
		Port:       8009,
		InfoFields: []string{"fn=" + planted, "ca=2"},
	})
	awaitSnapshotWithName(t, ch, planted, true)

	// Health-check eviction pass: the planted device is unreachable.
	evictDeadChromecastDevices(func(address string) bool { return address != "192.0.2.30:8009" })
	awaitSnapshotWithName(t, ch, planted, false)

	ccMu.Lock()
	_, stillCached := chromeCastDevices["192.0.2.30:8009"]
	ccMu.Unlock()
	if stillCached {
		t.Fatal("evicted Chromecast still cached")
	}
}

func TestChromecastAudioOnlyFlagPropagates(t *testing.T) {
	f := newDiscoveryFeed(func() []Device {
		return []Device{{Name: "Speaker", Addr: "http://192.0.2.31:8009", Type: DeviceTypeChromecast, IsAudioOnly: true}}
	})
	ch, cancel := f.subscribe(1)
	defer cancel()
	snapshot := receiveSnapshot(t, ch)
	if len(snapshot) != 1 || !snapshot[0].IsAudioOnly || !strings.EqualFold(snapshot[0].Type, DeviceTypeChromecast) {
		t.Fatalf("snapshot = %v, want one audio-only Chromecast", snapshot)
	}
}

func TestRefreshDiscoveryCoalescesAndCompletes(t *testing.T) {
	t.Parallel()
	f := newDiscoveryFeed(func() []Device { return nil })

	results := make(chan error, 2)
	for range 2 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			results <- f.refresh(ctx)
		}()
	}

	// Give both callers time to join the same in-flight operation, then signal
	// completed scans from both existing planes.
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	if f.inflight == nil {
		f.mu.Unlock()
		t.Fatal("no in-flight refresh operation")
	}
	f.mu.Unlock()
	f.notifyDLNAScan()
	f.notifyChromecastScan()

	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("refresh error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("refresh did not complete")
		}
	}
}

func TestRefreshDiscoveryHonorsCallerContext(t *testing.T) {
	t.Parallel()
	f := newDiscoveryFeed(func() []Device { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := f.refresh(ctx); err != context.DeadlineExceeded {
		t.Fatalf("refresh error = %v, want context deadline", err)
	}
	// Unblock the in-flight wait so its goroutine exits.
	f.notifyDLNAScan()
	f.notifyChromecastScan()
}
