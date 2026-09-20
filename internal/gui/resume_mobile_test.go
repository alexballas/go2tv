//go:build android || ios

package gui

import (
	"os"
	"testing"
	"time"

	"github.com/alexballas/refyne/v2/storage"
)

type fakeMobileResumePrefs struct {
	values map[string]string
}

func newFakeMobileResumePrefs() *fakeMobileResumePrefs {
	return &fakeMobileResumePrefs{values: make(map[string]string)}
}

func (p *fakeMobileResumePrefs) String(key string) string {
	return p.values[key]
}

func (p *fakeMobileResumePrefs) SetString(key, value string) {
	p.values[key] = value
}

func TestMobileResumeStoreRoundTrip(t *testing.T) {
	store := newResumeStore(newFakeMobileResumePrefs())
	entry := resumeEntry{
		MediaURI:            "content://media/external/video/media/42",
		MediaKind:           "video",
		LastPositionSeconds: 91,
		UpdatedAtUnix:       10,
	}
	if err := store.save(entry); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, found, err := store.find(entry.identity())
	if err != nil || !found {
		t.Fatalf("find: found=%v err=%v", found, err)
	}
	if got.LastPositionSeconds != entry.LastPositionSeconds {
		t.Fatalf("position = %d, want %d", got.LastPositionSeconds, entry.LastPositionSeconds)
	}
}

func TestResolveMobileResumeIdentityKeepsContentURI(t *testing.T) {
	uri, err := storage.ParseURI("content://media/external/video/media/42?token=stable")
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	identity, ok, err := resolveMobileResumeIdentity(uri, "video")
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if identity.MediaURI != uri.String() {
		t.Fatalf("uri = %q, want %q", identity.MediaURI, uri.String())
	}
	if identity.Size != 0 || identity.MTimeUnixNano != 0 {
		t.Fatalf("content identity unexpectedly has local metadata: %+v", identity)
	}
}

func TestResolveMobileResumeIdentityRejectsReplacedFile(t *testing.T) {
	path := t.TempDir() + "/episode.mp4"
	if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("write first: %v", err)
	}
	uri := storage.NewFileURI(path)
	first, ok, err := resolveMobileResumeIdentity(uri, "video")
	if err != nil || !ok {
		t.Fatalf("first identity: ok=%v err=%v", ok, err)
	}
	if err := os.WriteFile(path, []byte("replacement file"), 0o600); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	if err := os.Chtimes(path, time.Now(), time.Now().Add(time.Second)); err != nil {
		t.Fatalf("change mtime: %v", err)
	}
	second, ok, err := resolveMobileResumeIdentity(uri, "video")
	if err != nil || !ok {
		t.Fatalf("second identity: ok=%v err=%v", ok, err)
	}
	if first == second {
		t.Fatalf("replaced file retained identity: %+v", first)
	}
}

func TestMobileResumeStartCalculations(t *testing.T) {
	ffmpeg, direct := computeResumeStart(0, 40, true)
	if ffmpeg != 40 || direct != 0 {
		t.Fatalf("transcode = (%d, %d), want (40, 0)", ffmpeg, direct)
	}
	ffmpeg, direct = computeResumeStart(0, 40, false)
	if ffmpeg != 0 || direct != 40 {
		t.Fatalf("direct = (%d, %d), want (0, 40)", ffmpeg, direct)
	}
	if got := computeChromecastResumeStart(73, 40); got != 73 {
		t.Fatalf("explicit seek = %d, want 73", got)
	}
}

func TestMobileResumeCompletionRemovesEntry(t *testing.T) {
	if !shouldRemoveResumeEntry(96, 100) {
		t.Fatal("expected near-complete media to remove resume entry")
	}
	if shouldRemoveResumeEntry(20, 100) {
		t.Fatal("unexpected removal for in-progress media")
	}
}
