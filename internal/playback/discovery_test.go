package playback

import (
	"context"
	"strings"
	"sync"
	"testing"
)

type discoveryScanner struct {
	mu                sync.Mutex
	devices           []Device
	gate              <-chan struct{}
	active, maxActive int
}

func (s *discoveryScanner) Scan(ctx context.Context) ([]Device, error) {
	s.mu.Lock()
	s.active++
	s.maxActive = max(s.maxActive, s.active)
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
		return nil, ctx.Err()
	case <-s.gate:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	return append([]Device(nil), s.devices...), nil
}

func (s *discoveryScanner) replace(devices ...Device) {
	s.mu.Lock()
	s.devices = devices
	s.mu.Unlock()
}

func TestDiscoveryRefreshSerializesAndMaintainsIdentity(t *testing.T) {
	gate := make(chan struct{})
	scanner := &discoveryScanner{
		devices: []Device{{Name: "TV", Protocol: "DLNA", Endpoint: "http://192.0.2.2"}},
		gate:    gate,
	}
	service := NewDiscoveryService(scanner, strings.NewReader(strings.Repeat("a", 16)+strings.Repeat("b", 16)), nil, 0)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := service.Refresh(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	close(gate)
	wg.Wait()

	first := service.Snapshot()
	if len(first) != 1 || first[0].ID == "" || scanner.maxActive != 1 {
		t.Fatalf("snapshot = %#v, concurrent scans = %d", first, scanner.maxActive)
	}
	oldID := first[0].ID
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := service.Snapshot()[0].ID; got != oldID {
		t.Fatalf("stable ID changed: %q -> %q", oldID, got)
	}

	scanner.replace(Device{Name: "Other", Protocol: "DLNA", Endpoint: "http://192.0.2.3"})
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	latest := service.Snapshot()
	if len(latest) != 1 || latest[0].ID == oldID {
		t.Fatalf("latest snapshot = %#v", latest)
	}
}

func TestDiscoverySubscriberReceivesLatestSnapshot(t *testing.T) {
	gate := make(chan struct{})
	close(gate)
	scanner := &discoveryScanner{devices: []Device{{Name: "First", Protocol: "DLNA", Endpoint: "http://192.0.2.2"}}, gate: gate}
	service := NewDiscoveryService(scanner, strings.NewReader(strings.Repeat("a", 16)+strings.Repeat("b", 16)), nil, 0)
	updates, unsubscribe := service.Subscribe(1)
	defer unsubscribe()

	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	scanner.replace(Device{Name: "Latest", Protocol: "DLNA", Endpoint: "http://192.0.2.3"})
	if err := service.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	update := <-updates
	if len(update) != 1 || update[0].Name != "Latest" {
		t.Fatalf("subscriber update = %#v", update)
	}
}
