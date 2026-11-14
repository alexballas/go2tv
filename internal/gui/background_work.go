package gui

import (
	"context"
	"sync"
)

// backgroundWork prevents shutdown from racing new probes or playback startup.
// Workers finish after subprocesses are reaped, without waiting for UI callbacks.
type backgroundWork struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	stopping bool
	wg       sync.WaitGroup
}

func (w *backgroundWork) begin(parent context.Context) (context.Context, context.CancelFunc, func(), bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopping {
		return nil, nil, nil, false
	}
	if w.ctx == nil {
		w.ctx, w.cancel = context.WithCancel(context.Background())
	}
	ctx, cancel := context.WithCancel(w.ctx)
	if parent == nil {
		parent = context.Background()
	}
	stop := context.AfterFunc(parent, cancel)
	if parent.Err() != nil {
		cancel()
	}
	w.wg.Add(1)
	return ctx, cancel, func() { stop(); w.wg.Done() }, true
}

func (w *backgroundWork) stop() {
	w.mu.Lock()
	w.stopping = true
	if w.cancel != nil {
		w.cancel()
	}
	w.mu.Unlock()
}

func (w *backgroundWork) wait() { w.wg.Wait() }

// beginWork tracks an app-lifetime operation; finish cancels and releases it.
func (s *FyneScreen) beginWork() (context.Context, func(), bool) {
	ctx, cancel, done, ok := s.workers.begin(s.torrent.ctx)
	if !ok {
		return nil, nil, false
	}
	return ctx, func() { cancel(); done() }, true
}
