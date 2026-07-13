package devices

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

type serviceScanner struct {
	mu                sync.Mutex
	devices           []Device
	active, maxActive int
}

func (s *serviceScanner) Scan(ctx context.Context) ([]Device, error) {
	s.mu.Lock()
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
		return nil, ctx.Err()
	case <-time.After(time.Millisecond):
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	return append([]Device(nil), s.devices...), nil
}

func TestDiscoveryServiceStableIDsLatestSelectionAndSerialization(t *testing.T) {
	scanner := &serviceScanner{devices: []Device{{Name: "TV", Addr: "http://192.0.2.2", Type: DeviceTypeDLNA}}}
	s := NewDiscoveryService(scanner, time.Hour, strings.NewReader(strings.Repeat("a", 16)+strings.Repeat("b", 16)))
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Refresh(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	first := s.Snapshot()
	if len(first) != 1 || first[0].ID == "" {
		t.Fatalf("snapshot %#v", first)
	}
	if scanner.maxActive != 1 {
		t.Fatalf("concurrent scans %d", scanner.maxActive)
	}
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot()[0].ID; got != first[0].ID {
		t.Fatalf("ID changed %q -> %q", first[0].ID, got)
	}
	oldID := first[0].ID
	scanner.mu.Lock()
	scanner.devices = []Device{{Name: "Other", Addr: "http://192.0.2.3", Type: DeviceTypeDLNA}}
	scanner.mu.Unlock()
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Select(oldID); ok {
		t.Fatal("stale ID selected")
	}
}

func TestDiscoveryServiceRestartRotatesIDs(t *testing.T) {
	scanner := &serviceScanner{devices: []Device{{Name: "TV", Addr: "http://192.0.2.2", Type: DeviceTypeDLNA}}}
	one := NewDiscoveryService(scanner, time.Hour, strings.NewReader(strings.Repeat("a", 32)))
	two := NewDiscoveryService(scanner, time.Hour, strings.NewReader(strings.Repeat("b", 32)))
	if err := one.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := two.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if one.Snapshot()[0].ID == two.Snapshot()[0].ID {
		t.Fatal("process-lifetime IDs did not rotate")
	}
}

func TestDiscoveryServiceBroadcast(t *testing.T) {
	scanner := &serviceScanner{devices: []Device{{Name: "TV", Addr: "http://192.0.2.2", Type: DeviceTypeDLNA}}}
	s := NewDiscoveryService(scanner, time.Hour, strings.NewReader(strings.Repeat("a", 16)))
	one, cancelOne := s.Subscribe(1)
	defer cancelOne()
	two, cancelTwo := s.Subscribe(1)
	defer cancelTwo()
	if err := s.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, updates := range []<-chan []Device{one, two} {
		select {
		case devices := <-updates:
			if len(devices) != 1 {
				t.Fatalf("subscriber %d: %#v", i, devices)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d timeout", i)
		}
	}
}
