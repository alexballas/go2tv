package library

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRootsRejectDuplicateAndOverlap(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}

	for _, roots := range [][]string{{root, root}, {root, child}, {root, alias}} {
		if lib, err := Open(Config{Roots: roots}); !errors.Is(err, ErrInvalidRoot) {
			if lib != nil {
				_ = lib.Close()
			}
			t.Fatalf("Open(%q) error = %v", roots, err)
		}
	}
}

func TestBrowseFiltersAndMixedCaseExtensions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "movie.MP4"), "movie")
	writeFile(t, filepath.Join(root, "notes.txt"), "notes")
	writeFile(t, filepath.Join(root, ".hidden.mp3"), "hidden")
	if err := os.Mkdir(filepath.Join(root, ".secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeSpecialFiles(t, root)
	if err := os.Symlink(filepath.Join(t.TempDir(), "escape.mp4"), filepath.Join(root, "escape.mp4")); err != nil {
		t.Fatal(err)
	}

	lib, rootID := openTestLibrary(t, Config{Roots: []string{root}})
	page, err := lib.Browse(rootID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	got := entryNames(page.Entries)
	if !contains(got, "movie.MP4") || !contains(got, "folder") {
		t.Fatalf("entries = %q", got)
	}
	for _, rejected := range []string{"notes.txt", ".hidden.mp3", ".secret", "pipe", "socket", "escape.mp4"} {
		if contains(got, rejected) {
			t.Fatalf("unexpected entry %q in %q", rejected, got)
		}
	}
	for _, special := range []string{"pipe", "socket"} {
		file, _, openErr := lib.OpenForPlay(rootID, signedToken(lib.secret[:], rootID, special))
		if openErr == nil {
			_ = file.Close()
			t.Fatalf("special file %q opened", special)
		}
	}
}

func TestTraversalHiddenAndTamperedTokensRejected(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.mp4"), "ok")
	lib, rootID := openTestLibrary(t, Config{Roots: []string{root}})
	page, err := lib.Browse(rootID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	id := findEntry(t, page.Entries, "ok.mp4").ID

	middle := len(id) / 2
	tampered := id[:middle] + differentByte(id[middle]) + id[middle+1:]
	if _, _, err := lib.OpenForPlay(rootID, tampered); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("tamper error = %v", err)
	}
	for _, rel := range []string{"../ok.mp4", "dir/../ok.mp4", ".hidden/ok.mp4", "/ok.mp4"} {
		token := signedToken(lib.secret[:], rootID, rel)
		if _, _, err := lib.OpenForPlay(rootID, token); !errors.Is(err, ErrInvalidEntry) {
			t.Fatalf("rel %q error = %v", rel, err)
		}
	}
	long := strings.Repeat("a", MaxRelative+1)
	if _, _, err := lib.OpenForPlay(rootID, signedToken(lib.secret[:], rootID, long)); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("long token error = %v", err)
	}
}

func TestOpaqueInvalidUTF8Filename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("invalid UTF-8 filenames unsupported")
	}
	root := t.TempDir()
	raw := string([]byte{'b', 'a', 'd', 0xff, '.', 'm', 'p', '3'})
	writeFile(t, filepath.Join(root, raw), "opaque")
	lib, rootID := openTestLibrary(t, Config{Roots: []string{root}})
	page, err := lib.Browse(rootID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || !strings.Contains(page.Entries[0].Name, "�") || !utf8.ValidString(page.Entries[0].Name) {
		t.Fatalf("display name = %q", page.Entries)
	}
	file, _, err := lib.OpenForPlay(rootID, page.Entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "opaque" {
		t.Fatalf("read = %q, %v", data, err)
	}
}

func TestSymlinkAndRenameSwapsContained(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(root, "media.mp4"), "inside")
	writeFile(t, filepath.Join(outside, "outside.mp4"), "outside")
	lib, rootID := openTestLibrary(t, Config{Roots: []string{root}})
	page, err := lib.Browse(rootID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	id := findEntry(t, page.Entries, "media.mp4").ID
	if err := os.Remove(filepath.Join(root, "media.mp4")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside.mp4"), filepath.Join(root, "media.mp4")); err != nil {
		t.Fatal(err)
	}
	openers := []func(string, string) (*os.File, Metadata, error){lib.Select, lib.OpenForPlay, lib.OpenForAutoplay, lib.OpenDirect, lib.OpenTranscode}
	for _, open := range openers {
		if file, _, err := open(rootID, id); err == nil {
			_ = file.Close()
			t.Fatal("symlink escape opened")
		}
	}

	if err := os.Remove(filepath.Join(root, "media.mp4")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "media.mp4"), "replacement")
	file, _, err := lib.OpenDirect(rootID, id)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, _ := io.ReadAll(file)
	if string(data) != "replacement" {
		t.Fatalf("replacement read = %q", data)
	}
}

func TestRootHandleSurvivesRootRename(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "old.mp4"), "old")
	lib, rootID := openTestLibrary(t, Config{Roots: []string{root}})
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "new.mp4"), "new")
	page, err := lib.Browse(rootID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryNames(page.Entries); !contains(got, "old.mp4") || contains(got, "new.mp4") {
		t.Fatalf("entries after rename = %q", got)
	}
}

func TestCursorPagingOrderScanCapExpiryMutationCleanup(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"skip-a.txt", "first.mp3", "skip-b.txt", "second.mp3", "third.mp3"} {
		writeFile(t, filepath.Join(root, name), name)
	}
	lib, rootID := openTestLibrary(t, Config{Roots: []string{root}, ScanCap: 2, CursorTTL: 30 * time.Millisecond})
	expected := supportedDirectoryOrder(t, root)
	var got []string
	cursor := ""
	requests := 0
	for {
		page, err := lib.Browse(rootID, "", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		requests++
		got = append(got, entryNames(page.Entries)...)
		cursor = page.Cursor
		if cursor == "" {
			break
		}
	}
	if strings.Join(got, "\x00") != strings.Join(expected, "\x00") {
		t.Fatalf("paged order = %q, want %q", got, expected)
	}
	if requests < 3 || len(lib.cursors) != 0 {
		t.Fatalf("requests = %d, live = %d", requests, len(lib.cursors))
	}

	page, err := lib.Browse(rootID, "", "", 1)
	if err != nil || page.Cursor == "" {
		t.Fatalf("expiry page = %+v, %v", page, err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := lib.Browse(rootID, "", page.Cursor, 1); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("expiry error = %v", err)
	}
	if len(lib.cursors) != 0 {
		t.Fatalf("expired live cursors = %d", len(lib.cursors))
	}

	page, err = lib.Browse(rootID, "", "", 1)
	if err != nil || page.Cursor == "" {
		t.Fatalf("mutation page = %+v, %v", page, err)
	}
	writeFile(t, filepath.Join(root, "mutation.mp3"), "mutation")
	if _, err := lib.Browse(rootID, "", page.Cursor, 1); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("mutation error = %v", err)
	}
}

func TestCursorDirectoryRenameSwapExpired(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "a.mp3"), "a")
	writeFile(t, filepath.Join(dir, "b.mp3"), "b")
	lib, rootID := openTestLibrary(t, Config{Roots: []string{root}})
	rootPage, err := lib.Browse(rootID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	parentID := findEntry(t, rootPage.Entries, "dir").ID
	page, err := lib.Browse(rootID, parentID, "", 1)
	if err != nil || page.Cursor == "" {
		t.Fatalf("page = %+v, %v", page, err)
	}
	if err := os.Rename(dir, dir+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Browse(rootID, parentID, page.Cursor, 1); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("rename swap error = %v", err)
	}
}

func TestCursorLimit(t *testing.T) {
	root := t.TempDir()
	for i := range MaxCursors + 1 {
		dir := filepath.Join(root, fmt.Sprintf("dir-%02d", i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "a.mp3"), "a")
		writeFile(t, filepath.Join(dir, "b.mp3"), "b")
	}
	lib, rootID := openTestLibrary(t, Config{Roots: []string{root}})
	rootPage, err := lib.Browse(rootID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, entry := range rootPage.Entries {
		_, err := lib.Browse(rootID, entry.ID, "", 1)
		if i < MaxCursors && err != nil {
			t.Fatalf("cursor %d: %v", i, err)
		}
		if i == MaxCursors && !errors.Is(err, ErrCursorLimit) {
			t.Fatalf("cursor limit error = %v", err)
		}
	}
	if len(lib.cursors) != MaxCursors {
		t.Fatalf("live cursors = %d", len(lib.cursors))
	}
}

func TestFreshDirectAndTranscodeHandlesAndRelatedContainment(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(root, "movie.mp4"), "one")
	writeFile(t, filepath.Join(root, "movie.srt"), "subs")
	writeFile(t, filepath.Join(root, "movie.jpg"), "art")
	writeFile(t, filepath.Join(outside, "outside.srt"), "outside")
	lib, rootID := openTestLibrary(t, Config{Roots: []string{root}})
	page, err := lib.Browse(rootID, "", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	id := findEntry(t, page.Entries, "movie.mp4").ID
	direct, meta, err := lib.OpenDirect(rootID, id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.AbsolutePath() != filepath.Join(root, "movie.mp4") {
		t.Fatalf("absolute path = %q", meta.AbsolutePath())
	}
	transcode, _, err := lib.OpenTranscode(rootID, id)
	if err != nil {
		t.Fatal(err)
	}
	if direct == transcode {
		t.Fatal("handles reused")
	}
	_ = direct.Close()
	_ = transcode.Close()
	sidecar, _, err := lib.OpenSidecar(rootID, id, ".srt")
	if err != nil {
		t.Fatal(err)
	}
	_ = sidecar.Close()
	art, _, err := lib.OpenArtwork(rootID, id, ".jpg")
	if err != nil {
		t.Fatal(err)
	}
	_ = art.Close()
	if err := os.Remove(filepath.Join(root, "movie.srt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "outside.srt"), filepath.Join(root, "movie.srt")); err != nil {
		t.Fatal(err)
	}
	if file, _, err := lib.OpenSidecar(rootID, id, ".srt"); err == nil {
		_ = file.Close()
		t.Fatal("sidecar escape opened")
	}
}

func TestCloseClosesCursorsAndRoots(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.mp3"), "a")
	writeFile(t, filepath.Join(root, "b.mp3"), "b")
	lib, rootID := openTestLibraryNoCleanup(t, Config{Roots: []string{root}})
	page, err := lib.Browse(rootID, "", "", 1)
	if err != nil || page.Cursor == "" {
		t.Fatalf("page = %+v, %v", page, err)
	}
	state := lib.cursors[page.Cursor]
	if err := lib.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := state.dir.ReadDir(1); err == nil {
		t.Fatal("cursor directory remains open")
	}
	if _, err := lib.Browse(rootID, "", "", 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed browse error = %v", err)
	}
}

func openTestLibrary(t *testing.T, cfg Config) (*Library, string) {
	t.Helper()
	lib, id := openTestLibraryNoCleanup(t, cfg)
	t.Cleanup(func() { _ = lib.Close() })
	return lib, id
}

func openTestLibraryNoCleanup(t *testing.T, cfg Config) (*Library, string) {
	t.Helper()
	lib, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	roots := lib.Roots()
	if len(roots) != 1 || strings.Contains(roots[0].Name, cfg.Roots[0]) {
		_ = lib.Close()
		t.Fatalf("roots = %+v", roots)
	}
	return lib, roots[0].ID
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func makeSpecialFiles(t *testing.T, root string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	if err := makeFIFO(filepath.Join(root, "pipe")); err != nil {
		t.Fatal(err)
	}
	makeSocket(t, filepath.Join(root, "socket"))
}

func entryNames(entries []Entry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func supportedDirectoryOrder(t *testing.T, path string) []string {
	t.Helper()
	dir, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if supportedFile(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	return names
}

func contains(values []string, value string) bool {
	for _, got := range values {
		if got == value {
			return true
		}
	}
	return false
}

func findEntry(t *testing.T, entries []Entry, name string) Entry {
	t.Helper()
	for _, entry := range entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("entry %q missing from %+v", name, entries)
	return Entry{}
}

func signedToken(secret []byte, rootID, rel string) string {
	payload := []byte{tokenVersion, byte(len(rootID))}
	payload = append(payload, rootID...)
	payload = append(payload, rel...)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...))
}

func differentByte(value byte) string {
	if value == 'A' {
		return "B"
	}
	return "A"
}
