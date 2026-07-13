//go:build linux && !android

package mediamodel

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestQueueDisplayPath(t *testing.T) {
	portalPath := filepath.Join(t.TempDir(), "portal.mp4")
	if err := os.WriteFile(portalPath, nil, 0o600); err != nil {
		t.Fatalf("create file: %v", err)
	}
	if err := unix.Setxattr(portalPath, documentPortalHostPathXattr, []byte("/host/original.mp4"), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			t.Skipf("xattrs unsupported: %v", err)
		}
		t.Fatalf("set xattr: %v", err)
	}
	item, ok := NewQueueItem(portalPath)
	if !ok || item.Path() != portalPath || item.DisplayPath() != "/host/original.mp4" {
		t.Fatalf("item paths = %q, %q", item.Path(), item.DisplayPath())
	}
}

func TestQueueDisplayPathFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ordinary.mp4")
	if got := QueueDisplayPath(path); got != path {
		t.Fatalf("display = %q, want %q", got, path)
	}
}
