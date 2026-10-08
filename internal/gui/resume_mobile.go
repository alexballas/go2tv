//go:build android || ios

package gui

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/storage"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

const (
	rememberPlaybackPositionPref = "RememberPlaybackPosition"
	resumeHistoryPref            = "ResumePlaybackHistory"
	resumeHistoryVersion         = 1
	resumeHistoryMaxEntries      = 100
	resumeSaveInterval           = 5 * time.Second
	resumeMinDeltaSeconds        = 5
	resumeMinPositionSeconds     = 10
	resumeCompletionProgress     = 0.95
	resumeCompletionRemaining    = 30
	dlnaResumeRetryAttempts      = 12
	dlnaResumeRetryInterval      = time.Second
)

type resumePreferences interface {
	String(string) string
	SetString(string, string)
}

type resumeEntry struct {
	MediaURI            string `json:"media_uri"`
	Size                int64  `json:"size"`
	MTimeUnixNano       int64  `json:"mtime_unix_nano"`
	MediaKind           string `json:"media_kind"`
	LastPositionSeconds int    `json:"last_position_seconds"`
	UpdatedAtUnix       int64  `json:"updated_at_unix"`
}

type resumeHistory struct {
	Version int           `json:"version"`
	Entries []resumeEntry `json:"entries"`
}

type resumeIdentity struct {
	MediaURI      string
	Size          int64
	MTimeUnixNano int64
	MediaKind     string
}

type resumePlaybackSession struct {
	Identity         resumeIdentity
	Eligible         bool
	LastSavedAt      time.Time
	LastSavedSeconds int
}

type resumeStore struct {
	prefs resumePreferences
	key   string
}

type dlnaResumeClient interface {
	GetTransportInfo() ([]string, error)
	GetPositionInfo() ([]string, error)
	SeekSoapCall(string) error
}

func newResumeStore(prefs resumePreferences) *resumeStore {
	if prefs == nil {
		return nil
	}
	return &resumeStore{prefs: prefs, key: resumeHistoryPref}
}

func currentResumeStore() *resumeStore {
	app := fyne.CurrentApp()
	if app == nil {
		return nil
	}
	return newResumeStore(app.Preferences())
}

func rememberPlaybackPositionEnabled() bool {
	app := fyne.CurrentApp()
	return app != nil && app.Preferences().BoolWithFallback(rememberPlaybackPositionPref, false)
}

func (s *resumeStore) load() ([]resumeEntry, error) {
	if s == nil || s.prefs == nil {
		return nil, nil
	}
	raw := strings.TrimSpace(s.prefs.String(s.key))
	if raw == "" {
		return nil, nil
	}
	var history resumeHistory
	if err := json.Unmarshal([]byte(raw), &history); err == nil && history.Version == resumeHistoryVersion {
		return slices.Clone(history.Entries), nil
	}
	return nil, nil
}

func (s *resumeStore) saveEntries(entries []resumeEntry) error {
	if s == nil || s.prefs == nil {
		return nil
	}
	encoded, err := json.Marshal(resumeHistory{Version: resumeHistoryVersion, Entries: s.prune(entries)})
	if err != nil {
		return err
	}
	s.prefs.SetString(s.key, string(encoded))
	return nil
}

func (s *resumeStore) save(entry resumeEntry) error {
	entries, err := s.load()
	if err != nil {
		return err
	}
	for i := range entries {
		if resumeEntryMatches(entries[i], entry.identity()) {
			entries[i] = entry
			return s.saveEntries(entries)
		}
	}
	return s.saveEntries(append(entries, entry))
}

func (s *resumeStore) find(identity resumeIdentity) (resumeEntry, bool, error) {
	entries, err := s.load()
	if err != nil {
		return resumeEntry{}, false, err
	}
	for _, entry := range entries {
		if resumeEntryMatches(entry, identity) {
			return entry, true, nil
		}
	}
	return resumeEntry{}, false, nil
}

func (s *resumeStore) remove(identity resumeIdentity) error {
	entries, err := s.load()
	if err != nil {
		return err
	}
	filtered := entries[:0]
	for _, entry := range entries {
		if !resumeEntryMatches(entry, identity) {
			filtered = append(filtered, entry)
		}
	}
	return s.saveEntries(filtered)
}

func (s *resumeStore) clear() error {
	return s.saveEntries(nil)
}

func (s *resumeStore) prune(entries []resumeEntry) []resumeEntry {
	if len(entries) == 0 {
		return nil
	}
	pruned := slices.Clone(entries)
	slices.SortFunc(pruned, func(a, b resumeEntry) int {
		if a.UpdatedAtUnix != b.UpdatedAtUnix {
			return -cmp.Compare(a.UpdatedAtUnix, b.UpdatedAtUnix)
		}
		return strings.Compare(a.MediaURI, b.MediaURI)
	})
	if len(pruned) > resumeHistoryMaxEntries {
		return pruned[:resumeHistoryMaxEntries]
	}
	return pruned
}

func (e resumeEntry) identity() resumeIdentity {
	return resumeIdentity{MediaURI: e.MediaURI, Size: e.Size, MTimeUnixNano: e.MTimeUnixNano, MediaKind: e.MediaKind}
}

func resumeEntryMatches(entry resumeEntry, identity resumeIdentity) bool {
	return entry.MediaURI == identity.MediaURI && entry.Size == identity.Size &&
		entry.MTimeUnixNano == identity.MTimeUnixNano && entry.MediaKind == identity.MediaKind
}

// resolveMobileResumeIdentity keeps content URIs intact. They are opaque grants,
// so treating their full URI as the identity is safer than trying to reinterpret
// their provider-specific path. Local files additionally use their metadata so a
// replacement at the same path does not inherit an old position.
func resolveMobileResumeIdentity(uri fyne.URI, mediaKind string) (resumeIdentity, bool, error) {
	if uri == nil || strings.TrimSpace(mediaKind) == "" {
		return resumeIdentity{}, false, nil
	}
	identity := resumeIdentity{MediaURI: strings.TrimSpace(uri.String()), MediaKind: mediaKind}
	if identity.MediaURI == "" {
		return resumeIdentity{}, false, nil
	}
	if !strings.EqualFold(uri.Scheme(), "file") {
		return identity, true, nil
	}
	path := uri.Path()
	if path == "" {
		return resumeIdentity{}, false, nil
	}
	canonicalPath, err := filepath.Abs(path)
	if err != nil {
		return resumeIdentity{}, false, err
	}
	canonicalPath = filepath.Clean(canonicalPath)
	if resolvedPath, err := filepath.EvalSymlinks(canonicalPath); err == nil {
		canonicalPath = filepath.Clean(resolvedPath)
	}
	info, err := os.Stat(canonicalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return resumeIdentity{}, false, nil
		}
		return resumeIdentity{}, false, err
	}
	identity.MediaURI = storage.NewFileURI(canonicalPath).String()
	identity.Size = info.Size()
	identity.MTimeUnixNano = info.ModTime().UnixNano()
	return identity, true, nil
}

func resumeEligibleMediaKind(mediaType string) (string, bool) {
	switch {
	case strings.HasPrefix(mediaType, "video/"):
		return "video", true
	case strings.HasPrefix(mediaType, "audio/"):
		return "audio", true
	default:
		return "", false
	}
}

func computeResumeStart(existingSeek, storedResume int, transcode bool) (int, int) {
	if existingSeek > 0 {
		return existingSeek, 0
	}
	if storedResume < resumeMinPositionSeconds {
		return 0, 0
	}
	if transcode {
		return storedResume, 0
	}
	return 0, storedResume
}

func computeChromecastResumeStart(existingSeek, storedResume int) int {
	if existingSeek > 0 {
		return existingSeek
	}
	if storedResume < resumeMinPositionSeconds {
		return 0
	}
	return storedResume
}

func shouldRemoveResumeEntry(positionSeconds int, durationSeconds float64) bool {
	if positionSeconds < resumeMinPositionSeconds || durationSeconds <= 0 {
		return false
	}
	return float64(positionSeconds)/durationSeconds >= resumeCompletionProgress ||
		durationSeconds-float64(positionSeconds) <= resumeCompletionRemaining
}

func shouldPersistResumePosition(positionSeconds int, durationSeconds float64, lastSavedSeconds int, lastSavedAt, now time.Time, force bool) bool {
	if positionSeconds < resumeMinPositionSeconds || shouldRemoveResumeEntry(positionSeconds, durationSeconds) {
		return false
	}
	if force {
		return positionSeconds != lastSavedSeconds
	}
	return (lastSavedAt.IsZero() || now.Sub(lastSavedAt) >= resumeSaveInterval) && absInt(positionSeconds-lastSavedSeconds) >= resumeMinDeltaSeconds
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (s *FyneScreen) clearResumeSession() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.resumeSession = resumePlaybackSession{}
	s.mu.Unlock()
}

// prepareResumeSession returns a stored offset for the current local/content
// media. Call it once while setting up a new playback session, before starting
// the progress watcher. The caller applies the returned offset after the target
// renderer reports that media is ready.
func (s *FyneScreen) prepareResumeSession(mediaType string) int {
	s.clearResumeSession()
	if s == nil || !rememberPlaybackPositionEnabled() || s.mediafile == nil {
		return 0
	}
	if s.ExternalMediaURL != nil && s.ExternalMediaURL.Checked {
		return 0
	}
	mediaKind, ok := resumeEligibleMediaKind(mediaType)
	if !ok {
		return 0
	}
	identity, ok, err := resolveMobileResumeIdentity(s.mediafile, mediaKind)
	if err != nil || !ok {
		return 0
	}
	s.mu.Lock()
	s.resumeSession = resumePlaybackSession{Identity: identity, Eligible: true}
	s.mu.Unlock()
	store := currentResumeStore()
	if store == nil {
		return 0
	}
	entry, found, err := store.find(identity)
	if err != nil || !found || entry.LastPositionSeconds < resumeMinPositionSeconds {
		return 0
	}
	return entry.LastPositionSeconds
}

func (s *FyneScreen) persistResumeProgress(positionSeconds int, durationSeconds float64, force bool) {
	if s == nil || !rememberPlaybackPositionEnabled() {
		return
	}
	s.mu.RLock()
	session := s.resumeSession
	s.mu.RUnlock()
	if !session.Eligible {
		return
	}
	store := currentResumeStore()
	if store == nil {
		return
	}
	now := time.Now()
	if shouldRemoveResumeEntry(positionSeconds, durationSeconds) {
		_ = store.remove(session.Identity)
		s.recordResumeSave(positionSeconds, now)
		return
	}
	if !shouldPersistResumePosition(positionSeconds, durationSeconds, session.LastSavedSeconds, session.LastSavedAt, now, force) {
		return
	}
	entry := resumeEntry{MediaURI: session.Identity.MediaURI, Size: session.Identity.Size, MTimeUnixNano: session.Identity.MTimeUnixNano, MediaKind: session.Identity.MediaKind, LastPositionSeconds: positionSeconds, UpdatedAtUnix: now.Unix()}
	if store.save(entry) == nil {
		s.recordResumeSave(positionSeconds, now)
	}
}

func (s *FyneScreen) recordResumeSave(positionSeconds int, at time.Time) {
	s.mu.Lock()
	s.resumeSession.LastSavedAt = at
	s.resumeSession.LastSavedSeconds = positionSeconds
	s.mu.Unlock()
}

func (s *FyneScreen) persistDisplayedResumeProgress(force bool) {
	if s == nil || s.CurrentPos == nil {
		return
	}
	current, err := s.CurrentPos.Get()
	if err != nil {
		return
	}
	position, err := utils.ClockTimeToSeconds(current)
	if err != nil {
		return
	}
	duration := s.mediaDuration
	if s.EndPos != nil {
		if end, err := s.EndPos.Get(); err == nil {
			if seconds, err := utils.ClockTimeToSeconds(end); err == nil && seconds > 0 {
				duration = float64(seconds)
			}
		}
	}
	s.persistResumeProgress(position, duration, force)
}

func shouldAttemptInitialDLNASeek(client dlnaResumeClient) bool {
	transportInfo, err := client.GetTransportInfo()
	if err == nil && len(transportInfo) > 0 {
		switch strings.ToUpper(strings.TrimSpace(transportInfo[0])) {
		case "PLAYING", "PAUSED_PLAYBACK":
			return true
		case "TRANSITIONING", "STOPPED", "NO_MEDIA_PRESENT", "":
			return false
		default:
			return true
		}
	}
	_, err = client.GetPositionInfo()
	return err == nil
}

// applyInitialDLNAResume waits for the renderer to expose an active transport
// before issuing exactly one successful seek. It is safe to call immediately
// after SendtoTV; it never blocks the Fyne goroutine.
func (s *FyneScreen) applyInitialDLNAResume(tvdata *soapcalls.TVPayload, seconds int) {
	if s == nil || tvdata == nil || seconds < resumeMinPositionSeconds {
		return
	}
	go func() {
		reltime := utils.SecondsToClockTime(seconds)
		for attempt := range dlnaResumeRetryAttempts {
			if shouldAttemptInitialDLNASeek(tvdata) && tvdata.SeekSoapCall(reltime) == nil {
				return
			}
			if attempt+1 < dlnaResumeRetryAttempts {
				time.Sleep(dlnaResumeRetryInterval)
			}
		}
	}()
}
