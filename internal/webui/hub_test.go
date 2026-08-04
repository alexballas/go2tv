package webui

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go2tv.app/go2tv/v2/internal/controller"
)

type countingSnapshotController struct {
	calls atomic.Int32
}

func (c *countingSnapshotController) Snapshot(context.Context) (controller.Snapshot, error) {
	c.calls.Add(1)
	return controller.Snapshot{}, nil
}

func TestHubDoesNotSnapshotWithoutClients(t *testing.T) {
	control := &countingSnapshotController{}
	h := newHubWithConfig(control, nil, hubConfig{snapshotEvery: time.Millisecond})
	t.Cleanup(h.close)
	time.Sleep(20 * time.Millisecond)
	if calls := control.calls.Load(); calls != 0 {
		t.Fatalf("idle snapshot calls = %d, want 0", calls)
	}
}
