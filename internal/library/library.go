package library

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"go2tv.app/go2tv/v2/internal/mediamodel"
)

const (
	DefaultLimit = 100
	MaxLimit     = 200
	MaxRelative  = 4 << 10
	DefaultScan  = 1000
	DefaultTTL   = 60 * time.Second
	MaxCursors   = 32
	tokenVersion = 1
)

var (
	ErrClosed               = errors.New("library closed")
	ErrCursorExpired        = errors.New("cursor_expired")
	ErrCursorLimit          = errors.New("cursor limit reached")
	ErrInvalidEntry         = errors.New("invalid entry")
	ErrInvalidLimit         = errors.New("invalid limit")
	ErrInvalidRoot          = errors.New("invalid root")
	ErrNotDirectory         = errors.New("not a directory")
	ErrNotRegular           = errors.New("not a regular file")
	ErrUnsupportedExtension = errors.New("unsupported extension")
)

type Config struct {
	Roots     []string
	CursorTTL time.Duration
	ScanCap   int
	Random    io.Reader
}

type Root struct {
	ID   string
	Name string
}

type Entry struct {
	ID   string
	Name string
	Kind string
}

type Page struct {
	Entries []Entry
	Cursor  string
}

type Metadata struct {
	Name string
	Size int64
	path string
}

// AbsolutePath returns the canonical root joined with the validated entry path.
func (m Metadata) AbsolutePath() string { return m.path }

type rootState struct {
	id        string
	canonical string
	handle    *os.Root
}

type cursorState struct {
	id       string
	rootID   string
	rel      string
	dir      *os.File
	identity os.FileInfo
	stamp    fileStamp
	timer    *time.Timer
}

type fileStamp struct {
	mode    os.FileMode
	modTime time.Time
	size    int64
}

type Library struct {
	mu       sync.Mutex
	roots    map[string]*rootState
	rootList []Root
	secret   [32]byte
	random   io.Reader
	ttl      time.Duration
	scanCap  int
	cursors  map[string]*cursorState
	closed   bool
}

func Open(cfg Config) (*Library, error) {
	if len(cfg.Roots) == 0 {
		return nil, ErrInvalidRoot
	}
	if cfg.Random == nil {
		cfg.Random = rand.Reader
	}
	if cfg.CursorTTL == 0 {
		cfg.CursorTTL = DefaultTTL
	}
	if cfg.CursorTTL < 0 {
		return nil, ErrInvalidRoot
	}
	if cfg.ScanCap == 0 {
		cfg.ScanCap = DefaultScan
	}
	if cfg.ScanCap < 1 {
		return nil, ErrInvalidRoot
	}

	l := &Library{
		roots:   make(map[string]*rootState, len(cfg.Roots)),
		random:  cfg.Random,
		ttl:     cfg.CursorTTL,
		scanCap: cfg.ScanCap,
		cursors: make(map[string]*cursorState),
	}
	if _, err := io.ReadFull(l.random, l.secret[:]); err != nil {
		return nil, fmt.Errorf("library secret: %w", err)
	}

	states := make([]*rootState, 0, len(cfg.Roots))
	closeStates := func() {
		for _, state := range states {
			_ = state.handle.Close()
		}
	}
	for _, configured := range cfg.Roots {
		canonical, err := canonicalRoot(configured)
		if err != nil {
			closeStates()
			return nil, err
		}
		for _, existing := range states {
			if pathsOverlap(existing.canonical, canonical) {
				closeStates()
				return nil, ErrInvalidRoot
			}
		}
		handle, err := os.OpenRoot(canonical)
		if err != nil {
			closeStates()
			return nil, fmt.Errorf("open media root: %w", ErrInvalidRoot)
		}
		info, err := handle.Stat(".")
		if err != nil || !info.IsDir() {
			_ = handle.Close()
			closeStates()
			return nil, ErrInvalidRoot
		}
		for _, existing := range states {
			existingInfo, statErr := existing.handle.Stat(".")
			if statErr != nil || os.SameFile(existingInfo, info) {
				_ = handle.Close()
				closeStates()
				return nil, ErrInvalidRoot
			}
		}
		id, err := l.randomID()
		if err != nil {
			_ = handle.Close()
			closeStates()
			return nil, err
		}
		state := &rootState{id: id, canonical: canonical, handle: handle}
		states = append(states, state)
		l.roots[id] = state
		l.rootList = append(l.rootList, Root{ID: id, Name: displayName(filepath.Base(canonical))})
	}
	return l, nil
}

func (l *Library) Roots() []Root {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Root(nil), l.rootList...)
}

func (l *Library) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	var errs []error
	for id := range l.cursors {
		if err := l.closeCursorLocked(id); err != nil {
			errs = append(errs, err)
		}
	}
	for _, root := range l.roots {
		if err := root.handle.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	l.mu.Unlock()
	return errors.Join(errs...)
}

// Browse implements GET /api/library's root_id, parent_id, cursor, and limit contract.
func (l *Library) Browse(rootID, parentID, cursor string, limit int) (Page, error) {
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return Page{}, ErrInvalidLimit
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Page{}, ErrClosed
	}
	root, ok := l.roots[rootID]
	if !ok {
		return Page{}, ErrInvalidRoot
	}
	rel := "."
	if parentID != "" {
		var err error
		rel, err = l.decodeEntry(rootID, parentID)
		if err != nil {
			return Page{}, err
		}
	}

	var state *cursorState
	if cursor != "" {
		state = l.cursors[cursor]
		if state == nil || state.rootID != rootID || state.rel != rel {
			return Page{}, ErrCursorExpired
		}
		if !directoryUnchanged(root, state) {
			_ = l.closeCursorLocked(cursor)
			return Page{}, ErrCursorExpired
		}
	} else {
		file, err := openRootFile(root.handle, rel)
		if err != nil {
			return Page{}, mapOpenError(err)
		}
		info, err := file.Stat()
		if err != nil || !info.IsDir() {
			_ = file.Close()
			return Page{}, ErrNotDirectory
		}
		state = &cursorState{rootID: rootID, rel: rel, dir: file, identity: info, stamp: stamp(info)}
	}

	page, exhausted, err := l.readPage(root, state, limit)
	if err != nil {
		if state.id != "" {
			_ = l.closeCursorLocked(state.id)
		} else {
			_ = state.dir.Close()
		}
		return Page{}, err
	}
	if !directoryUnchanged(root, state) {
		if state.id != "" {
			_ = l.closeCursorLocked(state.id)
		} else {
			_ = state.dir.Close()
		}
		return Page{}, ErrCursorExpired
	}
	if exhausted {
		if state.id != "" {
			_ = l.closeCursorLocked(state.id)
		} else {
			_ = state.dir.Close()
		}
		return page, nil
	}
	if state.id == "" {
		if len(l.cursors) >= MaxCursors {
			_ = state.dir.Close()
			return Page{}, ErrCursorLimit
		}
		id, err := l.randomID()
		if err != nil {
			_ = state.dir.Close()
			return Page{}, err
		}
		state.id = id
		state.timer = time.AfterFunc(l.ttl, func() { l.expireCursor(id) })
		l.cursors[id] = state
	}
	page.Cursor = state.id
	return page, nil
}

func (l *Library) readPage(root *rootState, state *cursorState, limit int) (Page, bool, error) {
	page := Page{Entries: make([]Entry, 0, limit)}
	scanned := 0
	for len(page.Entries) < limit && scanned < l.scanCap {
		batchSize := min(limit-len(page.Entries), l.scanCap-scanned, 32)
		entries, err := state.dir.ReadDir(batchSize)
		scanned += len(entries)
		for _, dirEntry := range entries {
			entry, ok := l.entry(root, state.rel, dirEntry)
			if ok {
				page.Entries = append(page.Entries, entry)
			}
		}
		if errors.Is(err, io.EOF) {
			return page, true, nil
		}
		if err != nil {
			return Page{}, false, fmt.Errorf("read directory: %w", err)
		}
		if len(entries) == 0 {
			return page, true, nil
		}
	}
	return page, false, nil
}

func (l *Library) entry(root *rootState, parent string, dirEntry os.DirEntry) (Entry, bool) {
	name := dirEntry.Name()
	if hiddenName(name) {
		return Entry{}, false
	}
	rel := name
	if parent != "." {
		rel = filepath.Join(parent, name)
	}
	if err := validateRelative(rel); err != nil {
		return Entry{}, false
	}
	info, err := root.handle.Stat(rel)
	if err != nil {
		return Entry{}, false
	}
	kind := "directory"
	if !info.IsDir() {
		if !info.Mode().IsRegular() || !supportedFile(rel) {
			return Entry{}, false
		}
		kind = "file"
	}
	id, err := l.encodeEntry(root.id, rel)
	if err != nil {
		return Entry{}, false
	}
	return Entry{ID: id, Name: displayName(name), Kind: kind}, true
}

func (l *Library) Select(rootID, entryID string) (*os.File, Metadata, error) {
	return l.openRegular(rootID, entryID)
}

func (l *Library) OpenForPlay(rootID, entryID string) (*os.File, Metadata, error) {
	return l.openRegular(rootID, entryID)
}

func (l *Library) OpenForAutoplay(rootID, entryID string) (*os.File, Metadata, error) {
	return l.openRegular(rootID, entryID)
}

func (l *Library) OpenDirect(rootID, entryID string) (*os.File, Metadata, error) {
	return l.openRegular(rootID, entryID)
}

func (l *Library) OpenTranscode(rootID, entryID string) (*os.File, Metadata, error) {
	return l.openRegular(rootID, entryID)
}

func (l *Library) openRegular(rootID, entryID string) (*os.File, Metadata, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, Metadata{}, ErrClosed
	}
	root := l.roots[rootID]
	if root == nil {
		return nil, Metadata{}, ErrInvalidRoot
	}
	rel, err := l.decodeEntry(rootID, entryID)
	if err != nil {
		return nil, Metadata{}, err
	}
	if !supportedFile(rel) {
		return nil, Metadata{}, ErrUnsupportedExtension
	}
	file, err := openRootFile(root.handle, rel)
	if err != nil {
		return nil, Metadata{}, mapOpenError(err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, Metadata{}, ErrNotRegular
	}
	return file, Metadata{Name: displayName(filepath.Base(rel)), Size: info.Size(), path: filepath.Join(root.canonical, rel)}, nil
}

// OpenSidecar derives a sibling from a signed media entry and returns an already-open file.
func (l *Library) OpenSidecar(rootID, mediaID, extension string) (*os.File, Metadata, error) {
	if !strings.EqualFold(extension, ".srt") && !strings.EqualFold(extension, ".vtt") {
		return nil, Metadata{}, ErrUnsupportedExtension
	}
	return l.openRelated(rootID, mediaID, extension)
}

// OpenArtwork derives a sibling from a signed media entry and returns an already-open file.
func (l *Library) OpenArtwork(rootID, mediaID, extension string) (*os.File, Metadata, error) {
	switch strings.ToLower(extension) {
	case ".jpg", ".jpeg", ".png":
		return l.openRelated(rootID, mediaID, extension)
	default:
		return nil, Metadata{}, ErrUnsupportedExtension
	}
}

// OpenNamedArtwork opens a known artwork filename beside a signed media entry.
func (l *Library) OpenNamedArtwork(rootID, mediaID, name string) (*os.File, Metadata, error) {
	if filepath.Base(name) != name {
		return nil, Metadata{}, ErrInvalidEntry
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png":
	default:
		return nil, Metadata{}, ErrUnsupportedExtension
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, Metadata{}, ErrClosed
	}
	root := l.roots[rootID]
	if root == nil {
		return nil, Metadata{}, ErrInvalidRoot
	}
	mediaRel, err := l.decodeEntry(rootID, mediaID)
	if err != nil {
		return nil, Metadata{}, err
	}
	rel := filepath.Join(filepath.Dir(mediaRel), name)
	if err := validateRelative(rel); err != nil {
		return nil, Metadata{}, err
	}
	file, err := openRootFile(root.handle, rel)
	if err != nil {
		return nil, Metadata{}, mapOpenError(err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, Metadata{}, ErrNotRegular
	}
	return file, Metadata{Name: displayName(filepath.Base(rel)), Size: info.Size()}, nil
}

func (l *Library) openRelated(rootID, mediaID, extension string) (*os.File, Metadata, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, Metadata{}, ErrClosed
	}
	root := l.roots[rootID]
	if root == nil {
		return nil, Metadata{}, ErrInvalidRoot
	}
	mediaRel, err := l.decodeEntry(rootID, mediaID)
	if err != nil {
		return nil, Metadata{}, err
	}
	rel := strings.TrimSuffix(mediaRel, filepath.Ext(mediaRel)) + extension
	if err := validateRelative(rel); err != nil {
		return nil, Metadata{}, err
	}
	file, err := openRootFile(root.handle, rel)
	if err != nil {
		return nil, Metadata{}, mapOpenError(err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, Metadata{}, ErrNotRegular
	}
	return file, Metadata{Name: displayName(filepath.Base(rel)), Size: info.Size()}, nil
}

func (l *Library) encodeEntry(rootID, rel string) (string, error) {
	if err := validateRelative(rel); err != nil {
		return "", err
	}
	if len(rel) > MaxRelative || len(rootID) > 255 {
		return "", ErrInvalidEntry
	}
	payload := make([]byte, 2+len(rootID)+len(rel))
	payload[0] = tokenVersion
	payload[1] = byte(len(rootID))
	copy(payload[2:], rootID)
	copy(payload[2+len(rootID):], rel)
	mac := hmac.New(sha256.New, l.secret[:])
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...)), nil
}

func (l *Library) decodeEntry(rootID, token string) (string, error) {
	if len(token) > base64.RawURLEncoding.EncodedLen(MaxRelative+2+255+sha256.Size) {
		return "", ErrInvalidEntry
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) < 2+sha256.Size {
		return "", ErrInvalidEntry
	}
	payload, gotMAC := decoded[:len(decoded)-sha256.Size], decoded[len(decoded)-sha256.Size:]
	mac := hmac.New(sha256.New, l.secret[:])
	_, _ = mac.Write(payload)
	if !hmac.Equal(gotMAC, mac.Sum(nil)) || payload[0] != tokenVersion {
		return "", ErrInvalidEntry
	}
	rootLen := int(payload[1])
	if len(payload) < 2+rootLen || string(payload[2:2+rootLen]) != rootID {
		return "", ErrInvalidEntry
	}
	rel := string(payload[2+rootLen:])
	if len(rel) > MaxRelative || validateRelative(rel) != nil {
		return "", ErrInvalidEntry
	}
	return rel, nil
}

func (l *Library) expireCursor(id string) {
	l.mu.Lock()
	_ = l.closeCursorLocked(id)
	l.mu.Unlock()
}

func (l *Library) closeCursorLocked(id string) error {
	state := l.cursors[id]
	if state == nil {
		return nil
	}
	delete(l.cursors, id)
	if state.timer != nil {
		state.timer.Stop()
	}
	return state.dir.Close()
}

func (l *Library) randomID() (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(l.random, value[:]); err != nil {
		return "", fmt.Errorf("library random ID: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func canonicalRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", ErrInvalidRoot
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", ErrInvalidRoot
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", ErrInvalidRoot
	}
	return filepath.Clean(canonical), nil
}

func pathsOverlap(a, b string) bool {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))) {
			return true
		}
	}
	return false
}

func validateRelative(rel string) error {
	if rel == "." {
		return nil
	}
	if rel == "" || len(rel) > MaxRelative || strings.IndexByte(rel, 0) >= 0 || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return ErrInvalidEntry
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || hiddenName(part) {
			return ErrInvalidEntry
		}
	}
	if filepath.Clean(rel) != rel {
		return ErrInvalidEntry
	}
	return nil
}

func hiddenName(name string) bool {
	return len(name) > 0 && name[0] == '.'
}

func supportedFile(path string) bool {
	return mediamodel.KindForPath(path) != mediamodel.MediaKindUnknown || mediamodel.IsSRTPath(path) || mediamodel.IsVTTPath(path)
}

func displayName(raw string) string {
	valid := strings.ToValidUTF8(raw, "�")
	return strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) {
			return '�'
		}
		return r
	}, valid)
}

func stamp(info os.FileInfo) fileStamp {
	return fileStamp{mode: info.Mode(), modTime: info.ModTime(), size: info.Size()}
}

func directoryUnchanged(root *rootState, state *cursorState) bool {
	info, err := root.handle.Stat(state.rel)
	return err == nil && info.IsDir() && os.SameFile(info, state.identity) && stamp(info) == state.stamp
}

func mapOpenError(err error) error {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return ErrInvalidEntry
	}
	return fmt.Errorf("open entry: %w", ErrInvalidEntry)
}
