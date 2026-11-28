//go:build !(android || ios)

package gui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/alexballas/refyne/v2"
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
	MediaPath           string `json:"media_path"`
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
	MediaPath     string
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
	SeekSoapCall(reltime string) error
}

func newResumeStore(prefs resumePreferences) *resumeStore {
	if prefs == nil {
		return nil
	}

	return &resumeStore{
		prefs: prefs,
		key:   resumeHistoryPref,
	}
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
	if app == nil {
		return false
	}

	return app.Preferences().BoolWithFallback(rememberPlaybackPositionPref, false)
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

	var legacy []resumeEntry
	if err := json.Unmarshal([]byte(raw), &legacy); err == nil {
		return slices.Clone(legacy), nil
	}

	return nil, nil
}

func (s *resumeStore) saveEntries(entries []resumeEntry) error {
	if s == nil || s.prefs == nil {
		return nil
	}

	history := resumeHistory{
		Version: resumeHistoryVersion,
		Entries: s.prune(entries),
	}

	encoded, err := json.Marshal(history)
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

	replaced := false
	for i := range entries {
		if resumeEntryMatches(entries[i], entry.identity()) {
			entries[i] = entry
			replaced = true
			break
		}
	}

	if !replaced {
		entries = append(entries, entry)
	}

	return s.saveEntries(entries)
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
		switch {
		case a.UpdatedAtUnix > b.UpdatedAtUnix:
			return -1
		case a.UpdatedAtUnix < b.UpdatedAtUnix:
			return 1
		default:
			return strings.Compare(a.MediaPath, b.MediaPath)
		}
	})

	if len(pruned) > resumeHistoryMaxEntries {
		pruned = pruned[:resumeHistoryMaxEntries]
	}

	return pruned
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
		if resumeEntryMatches(entry, identity) {
			continue
		}
		filtered = append(filtered, entry)
	}

	return s.saveEntries(filtered)
}

func (e resumeEntry) identity() resumeIdentity {
	return resumeIdentity{
		MediaPath:     e.MediaPath,
		Size:          e.Size,
		MTimeUnixNano: e.MTimeUnixNano,
		MediaKind:     e.MediaKind,
	}
}

func resumeEntryMatches(entry resumeEntry, identity resumeIdentity) bool {
	return entry.MediaPath == identity.MediaPath &&
		entry.Size == identity.Size &&
		entry.MTimeUnixNano == identity.MTimeUnixNano &&
		entry.MediaKind == identity.MediaKind
}

func resolveResumeIdentity(mediaPath string, mediaKind string) (resumeIdentity, bool, error) {
	if strings.TrimSpace(mediaPath) == "" || strings.TrimSpace(mediaKind) == "" {
		return resumeIdentity{}, false, nil
	}

	absPath, err := filepath.Abs(mediaPath)
	if err != nil {
		return resumeIdentity{}, false, err
	}

	canonicalPath := filepath.Clean(absPath)
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

	return resumeIdentity{
		MediaPath:     canonicalizeResumePath(canonicalPath, runtime.GOOS),
		Size:          info.Size(),
		MTimeUnixNano: info.ModTime().UnixNano(),
		MediaKind:     mediaKind,
	}, true, nil
}

func canonicalizeResumePath(path string, goos string) string {
	out := filepath.Clean(path)
	if goos == "windows" {
		out = strings.ToLower(out)
	}

	return out
}

func resumeEligibleForPlayback(external bool, screencast bool, rtmp bool, mediaType string) (string, bool) {
	if external || screencast || rtmp {
		return "", false
	}

	return resumeEligibleMediaKind(mediaType)
}

func computeResumeStart(existingSeek int, storedResume int, transcode bool) (int, int) {
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

func computeChromecastResumeStart(existingSeek int, storedResume int) int {
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

	progress := float64(positionSeconds) / durationSeconds
	remaining := durationSeconds - float64(positionSeconds)

	return progress >= resumeCompletionProgress || remaining <= resumeCompletionRemaining
}

func shouldPersistResumePosition(positionSeconds int, durationSeconds float64, lastSavedSeconds int, lastSavedAt time.Time, now time.Time, force bool) bool {
	if positionSeconds < resumeMinPositionSeconds {
		return false
	}

	if shouldRemoveResumeEntry(positionSeconds, durationSeconds) {
		return false
	}

	if force {
		return positionSeconds != lastSavedSeconds
	}

	if !lastSavedAt.IsZero() && now.Sub(lastSavedAt) < resumeSaveInterval {
		return false
	}

	if absInt(positionSeconds-lastSavedSeconds) < resumeMinDeltaSeconds {
		return false
	}

	return true
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}

	return v
}

func (s *FyneScreen) clearResumeSession() {
	s.mu.Lock()
	s.resumeSession = resumePlaybackSession{}
	s.mu.Unlock()
}

func (s *FyneScreen) prepareResumeSession(mediaType string) int {
	s.clearResumeSession()

	if !rememberPlaybackPositionEnabled() {
		return 0
	}

	if s == nil {
		return 0
	}

	external := s.ExternalMediaURL != nil && s.ExternalMediaURL.Checked
	rtmp := s.rtmpServerCheck != nil && s.rtmpServerCheck.Checked
	mediaKind, ok := resumeEligibleForPlayback(external, s.Screencast, rtmp, mediaType)
	if !ok {
		return 0
	}

	identity, ok, err := resolveResumeIdentity(s.mediafile, mediaKind)
	if err != nil || !ok {
		return 0
	}

	s.mu.Lock()
	s.resumeSession = resumePlaybackSession{
		Identity: identity,
		Eligible: true,
	}
	s.mu.Unlock()

	store := currentResumeStore()
	if store == nil {
		return 0
	}

	entry, found, err := store.find(identity)
	if err != nil || !found {
		return 0
	}

	if entry.LastPositionSeconds < resumeMinPositionSeconds {
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
		s.mu.Lock()
		s.resumeSession.LastSavedAt = now
		s.resumeSession.LastSavedSeconds = positionSeconds
		s.mu.Unlock()
		return
	}

	if !shouldPersistResumePosition(positionSeconds, durationSeconds, session.LastSavedSeconds, session.LastSavedAt, now, force) {
		return
	}

	entry := resumeEntry{
		MediaPath:           session.Identity.MediaPath,
		Size:                session.Identity.Size,
		MTimeUnixNano:       session.Identity.MTimeUnixNano,
		MediaKind:           session.Identity.MediaKind,
		LastPositionSeconds: positionSeconds,
		UpdatedAtUnix:       now.Unix(),
	}
	if err := store.save(entry); err != nil {
		return
	}

	s.mu.Lock()
	s.resumeSession.LastSavedAt = now
	s.resumeSession.LastSavedSeconds = positionSeconds
	s.mu.Unlock()
}

func (s *FyneScreen) persistDisplayedResumeProgress(force bool) {
	if s == nil {
		return
	}

	positionSeconds, durationSeconds := s.displayedResumeProgress()
	s.persistResumeProgress(positionSeconds, durationSeconds, force)
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

func (s *FyneScreen) applyInitialDLNAResume(tvdata *soapcalls.TVPayload, seconds int) {
	if s == nil || tvdata == nil || seconds < resumeMinPositionSeconds {
		return
	}

	go func() {
		reltime := utils.SecondsToClockTime(seconds)

		for attempt := range dlnaResumeRetryAttempts {
			if shouldAttemptInitialDLNASeek(tvdata) {
				if err := tvdata.SeekSoapCall(reltime); err == nil {
					return
				}
			}

			if attempt+1 < dlnaResumeRetryAttempts {
				time.Sleep(dlnaResumeRetryInterval)
			}
		}
	}()
}

func (s *FyneScreen) displayedResumeProgress() (int, float64) {
	if s == nil || s.CurrentPos == nil {
		return 0, 0
	}

	currentText, err := s.CurrentPos.Get()
	if err != nil {
		return 0, 0
	}

	positionSeconds, err := utils.ClockTimeToSeconds(currentText)
	if err != nil {
		return 0, 0
	}

	if s.EndPos == nil {
		if s.mediaDuration > 0 {
			return positionSeconds, s.mediaDuration
		}
		return positionSeconds, 0
	}

	endText, err := s.EndPos.Get()
	if err != nil {
		return positionSeconds, 0
	}

	durationSeconds, err := utils.ClockTimeToSeconds(endText)
	if err == nil && durationSeconds > 0 {
		return positionSeconds, float64(durationSeconds)
	}

	if s.mediaDuration > 0 {
		return positionSeconds, s.mediaDuration
	}

	return positionSeconds, 0
}
