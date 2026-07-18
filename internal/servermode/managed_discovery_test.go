//go:build !(android || ios)

package servermode

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/managedsession"
)

func wireDevice(name, endpoint string) managedsession.Device {
	return managedsession.Device{Name: name, Protocol: "DLNA", Endpoint: endpoint}
}

func TestManagedDiscoveryStableIDsAcrossRename(t *testing.T) {
	t.Parallel()
	d := newManagedDiscovery(func(string) error { return nil })
	d.ApplySnapshot(1, []managedsession.Device{wireDevice("TV", "http://192.168.1.20:1400/device.xml")})
	first := d.Snapshot()
	if len(first) != 1 || first[0].ID == "" {
		t.Fatalf("snapshot = %+v", first)
	}
	d.ApplySnapshot(2, []managedsession.Device{{Name: "TV Renamed", Protocol: "DLNA", Endpoint: "http://192.168.1.20:1400/device.xml", AudioOnly: true}})
	second := d.Snapshot()
	if len(second) != 1 || second[0].ID != first[0].ID {
		t.Fatalf("ID changed across rename: %+v vs %+v", first, second)
	}
	if second[0].Name != "TV Renamed" || !second[0].AudioOnly {
		t.Fatalf("update not applied: %+v", second)
	}
}

func TestManagedDiscoveryRemovesAbsentAndAcceptsEmpty(t *testing.T) {
	t.Parallel()
	d := newManagedDiscovery(func(string) error { return nil })
	d.ApplySnapshot(1, []managedsession.Device{
		wireDevice("A", "http://192.168.1.20:1400/a.xml"),
		wireDevice("B", "http://192.168.1.21:1400/b.xml"),
	})
	d.ApplySnapshot(2, []managedsession.Device{wireDevice("A", "http://192.168.1.20:1400/a.xml")})
	if snapshot := d.Snapshot(); len(snapshot) != 1 || snapshot[0].Name != "A" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	d.ApplySnapshot(3, nil)
	if snapshot := d.Snapshot(); len(snapshot) != 0 {
		t.Fatalf("snapshot after empty = %+v", snapshot)
	}
}

func TestManagedDiscoveryIgnoresStaleRevisions(t *testing.T) {
	t.Parallel()
	d := newManagedDiscovery(func(string) error { return nil })
	d.ApplySnapshot(5, []managedsession.Device{wireDevice("A", "http://192.168.1.20:1400/a.xml")})
	d.ApplySnapshot(4, nil)
	if snapshot := d.Snapshot(); len(snapshot) != 1 {
		t.Fatalf("stale snapshot applied: %+v", snapshot)
	}
}

func TestManagedDiscoverySubscribePublishes(t *testing.T) {
	t.Parallel()
	d := newManagedDiscovery(func(string) error { return nil })
	ch, cancel := d.Subscribe(1)
	defer cancel()
	d.ApplySnapshot(1, []managedsession.Device{wireDevice("A", "http://192.168.1.20:1400/a.xml")})
	select {
	case snapshot := <-ch:
		if len(snapshot) != 1 || snapshot[0].Name != "A" {
			t.Fatalf("snapshot = %+v", snapshot)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no snapshot published")
	}
	d.ApplySnapshot(2, nil)
	select {
	case snapshot := <-ch:
		if len(snapshot) != 0 {
			t.Fatalf("snapshot = %+v, want empty", snapshot)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no removal published")
	}
}

func TestManagedDiscoveryRefreshCoalesces(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var requests []string
	d := newManagedDiscovery(func(id string) error {
		mu.Lock()
		requests = append(requests, id)
		mu.Unlock()
		return nil
	})

	const callers = 4
	results := make(chan error, callers)
	for range callers {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			results <- d.Refresh(ctx)
		}()
	}

	var requestID string
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		if len(requests) > 0 {
			requestID = requests[0]
			mu.Unlock()
			break
		}
		mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("no pipe refresh request sent")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Let the remaining callers join the outstanding request.
	time.Sleep(50 * time.Millisecond)

	d.ApplyRefreshResult(requestID, 9, []managedsession.Device{wireDevice("A", "http://192.168.1.20:1400/a.xml")}, "")
	for range callers {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("Refresh error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Refresh caller never released")
		}
	}
	mu.Lock()
	sent := len(requests)
	mu.Unlock()
	if sent != 1 {
		t.Fatalf("pipe refresh requests = %d, want 1", sent)
	}
	if snapshot := d.Snapshot(); len(snapshot) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestManagedDiscoveryLateResultDropped(t *testing.T) {
	t.Parallel()
	sent := make(chan string, 1)
	d := newManagedDiscovery(func(id string) error {
		sent <- id
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := d.Refresh(ctx)
	if !errors.Is(err, errManagedRefreshTimeout) {
		t.Fatalf("Refresh error = %v, want timeout", err)
	}
	requestID := <-sent
	// Late result: no waiter remains, so it must not match a pending request,
	// but its snapshot data may still apply.
	d.ApplyRefreshResult(requestID, 1, nil, "")
	d.mu.Lock()
	pending := d.pending
	d.mu.Unlock()
	if pending != nil {
		t.Fatalf("pending request survived: %+v", pending)
	}
}

func TestManagedDiscoveryRefreshErrorCodes(t *testing.T) {
	t.Parallel()
	sent := make(chan string, 1)
	d := newManagedDiscovery(func(id string) error {
		sent <- id
		return nil
	})
	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result <- d.Refresh(ctx)
	}()
	requestID := <-sent
	time.Sleep(20 * time.Millisecond)
	d.ApplyRefreshResult(requestID, 0, nil, managedsession.RefreshErrorTimeout)
	select {
	case err := <-result:
		if !errors.Is(err, errManagedRefreshTimeout) {
			t.Fatalf("Refresh error = %v, want timeout code", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Refresh never returned")
	}
}

func TestManagedDiscoveryCloseUnblocksRefresh(t *testing.T) {
	t.Parallel()
	d := newManagedDiscovery(func(string) error { return nil })
	result := make(chan error, 1)
	go func() {
		result <- d.Refresh(context.Background())
	}()
	time.Sleep(20 * time.Millisecond)
	d.Close()
	select {
	case err := <-result:
		if !errors.Is(err, errManagedDiscoveryClosed) {
			t.Fatalf("Refresh error = %v, want closed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Refresh not unblocked by Close")
	}
	if err := d.Refresh(context.Background()); !errors.Is(err, errManagedDiscoveryClosed) {
		t.Fatalf("Refresh after Close = %v, want closed", err)
	}
}
