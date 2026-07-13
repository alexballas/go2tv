package playback

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

const defaultDiscoveryInterval = time.Second
const defaultDiscoveryRefreshTimeout = 5 * time.Second

// DiscoveryService serializes scans and keeps IDs stable for its lifetime.
type DiscoveryService struct {
	scanner  DiscoveryScanner
	random   Random
	clock    Clock
	interval time.Duration

	refreshMu      sync.Mutex
	mu             sync.RWMutex
	devices        []Device
	ids            map[string]string
	updates        chan []Device
	subscribers    map[uint64]chan []Device
	nextSubscriber uint64
	startOnce      sync.Once
}

func NewDiscoveryService(scanner DiscoveryScanner, random Random, clock Clock, interval time.Duration) *DiscoveryService {
	if random == nil {
		random = rand.Reader
	}
	if clock == nil {
		clock = realClock{}
	}
	if interval <= 0 {
		interval = defaultDiscoveryInterval
	}
	return &DiscoveryService{
		scanner: scanner, random: random, clock: clock, interval: interval,
		ids: make(map[string]string), updates: make(chan []Device, 1), subscribers: make(map[uint64]chan []Device),
	}
}

func (s *DiscoveryService) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.startOnce.Do(func() { go s.loop(ctx) })
}

func (s *DiscoveryService) loop(ctx context.Context) {
	_ = s.Refresh(ctx)
	ticker := s.clock.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			_ = s.Refresh(ctx)
		}
	}
}

func (s *DiscoveryService) Refresh(ctx context.Context) error {
	if s == nil || s.scanner == nil {
		return errors.New("discovery scanner unavailable")
	}
	if ctx == nil {
		return errors.New("discovery context required")
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	scanCtx, cancel := context.WithTimeout(ctx, defaultDiscoveryRefreshTimeout)
	defer cancel()
	devices, err := s.scanner.Scan(scanCtx)
	if err != nil {
		return fmt.Errorf("discovery scan: %w", err)
	}
	for i := range devices {
		key := devices[i].Protocol + "\x00" + devices[i].Endpoint
		id := s.ids[key]
		if id == "" {
			id, err = opaqueID(s.random)
			if err != nil {
				return fmt.Errorf("discovery id: %w", err)
			}
			s.ids[key] = id
		}
		devices[i].ID = id
	}
	s.mu.Lock()
	s.devices = slices.Clone(devices)
	snapshot := slices.Clone(s.devices)
	s.mu.Unlock()
	select {
	case s.updates <- snapshot:
	default:
		select {
		case <-s.updates:
		default:
		}
		select {
		case s.updates <- snapshot:
		default:
		}
	}
	s.mu.RLock()
	for _, subscriber := range s.subscribers {
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
	s.mu.RUnlock()
	return nil
}

func opaqueID(r Random) (string, error) {
	b := make([]byte, 16)
	if _, err := ioReadFull(r, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func ioReadFull(r Random, b []byte) (int, error) {
	off := 0
	for off < len(b) {
		n, err := r.Read(b[off:])
		if err != nil {
			return off, err
		}
		if n == 0 {
			return off, errors.New("random source returned no data")
		}
		off += n
	}
	return off, nil
}

func (s *DiscoveryService) Snapshot() []Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.devices)
}

func (s *DiscoveryService) Notifications() <-chan []Device { return s.updates }

func (s *DiscoveryService) Subscribe(buffer int) (<-chan []Device, func()) {
	if buffer <= 0 {
		buffer = 1
	}
	s.mu.Lock()
	s.nextSubscriber++
	id := s.nextSubscriber
	ch := make(chan []Device, buffer)
	s.subscribers[id] = ch
	snapshot := slices.Clone(s.devices)
	s.mu.Unlock()
	if len(snapshot) > 0 {
		ch <- snapshot
	}
	var once sync.Once
	return ch, func() { once.Do(func() { s.mu.Lock(); delete(s.subscribers, id); close(ch); s.mu.Unlock() }) }
}

func (s *DiscoveryService) Resolve(id string) (Device, bool) {
	for _, device := range s.Snapshot() {
		if device.ID == id {
			return device, true
		}
	}
	return Device{}, false
}
