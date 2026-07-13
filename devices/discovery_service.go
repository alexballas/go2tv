package devices

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"
)

type Scanner interface {
	Scan(context.Context) ([]Device, error)
}

type DiscoveryService struct {
	scanner        Scanner
	interval       time.Duration
	random         io.Reader
	refreshMu      sync.Mutex
	mu             sync.RWMutex
	devices        []Device
	ids            map[string]string
	notify         chan []Device
	subscribers    map[uint64]chan []Device
	nextSubscriber uint64
	startOnce      sync.Once
}

const discoveryRefreshTimeout = 5 * time.Second

func NewDiscoveryService(scanner Scanner, interval time.Duration, randomSource io.Reader) *DiscoveryService {
	if interval <= 0 {
		interval = time.Second
	}
	if randomSource == nil {
		randomSource = rand.Reader
	}
	return &DiscoveryService{scanner: scanner, interval: interval, random: randomSource, ids: make(map[string]string), notify: make(chan []Device, 1), subscribers: make(map[uint64]chan []Device)}
}

func (s *DiscoveryService) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.startOnce.Do(func() { go s.loop(ctx) })
}

func (s *DiscoveryService) loop(ctx context.Context) {
	_ = s.Refresh(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
	scanCtx, cancel := context.WithTimeout(ctx, discoveryRefreshTimeout)
	defer cancel()
	devices, err := s.scanner.Scan(scanCtx)
	if err != nil {
		return fmt.Errorf("discovery scan: %w", err)
	}
	for i := range devices {
		key := devices[i].Type + "\x00" + devices[i].Addr
		id := s.ids[key]
		if id == "" {
			buf := make([]byte, 16)
			if _, err := io.ReadFull(s.random, buf); err != nil {
				return fmt.Errorf("discovery id: %w", err)
			}
			id = hex.EncodeToString(buf)
			s.ids[key] = id
		}
		devices[i].ID = id
	}
	s.mu.Lock()
	s.devices = slices.Clone(devices)
	snapshot := slices.Clone(devices)
	s.mu.Unlock()
	select {
	case s.notify <- snapshot:
	default:
		select {
		case <-s.notify:
		default:
		}
		select {
		case s.notify <- snapshot:
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

func (s *DiscoveryService) Snapshot() []Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.devices)
}

func (s *DiscoveryService) Notifications() <-chan []Device { return s.notify }

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

// Select accepts only an ID present in the latest snapshot.
func (s *DiscoveryService) Select(id string) (Device, bool) {
	for _, device := range s.Snapshot() {
		if device.ID == id {
			return device, true
		}
	}
	return Device{}, false
}
