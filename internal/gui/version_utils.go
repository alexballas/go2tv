package gui

import (
	"errors"
	"strings"

	"net/http"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"golang.org/x/mod/semver"
)

// getLatestVersion fetches the latest version tag from go2tv.app.
func getLatestVersion() (string, error) {
	req, err := http.NewRequest("GET", "https://go2tv.app/latest", nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	return filepath.Base(resp.Request.URL.Path), nil
}

// normalize adds a "v" prefix to the version string if it's missing.
// The semver package strictly requires the "v" prefix (e.g., "v1.2.3").
func normalize(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

// parseVersion is a legacy helper now used just to validity check for "dev" versions in about.go
// We keep the signature but use semver.IsValid internally after normalization.
// It returns a dummy []int or nil to satisfy the existing caller usage pattern if we want to minimize churn,
// OR we can change the caller.
// Earlier I replaced the caller in about.go to: if _, err := parseVersion(s.version); err != nil { ... }
// So I will maintain the signature but implement it using semver to check validity.
// Actually, about.go ignored the []int, so returning nil slice with nil error is fine for success.
func parseVersion(version string) ([]int, error) {
	norm := normalize(version)
	if !semver.IsValid(norm) {
		return nil, errors.New("invalid semantic version")
	}
	// Return non-nil slice to avoid potential checks? No, caller ignores it.
	return []int{0}, nil
}

// silentCheckVersion performs a background version check and notifies the user
// if a new version is available, but only once per version.
func silentCheckVersion(s *FyneScreen) {
	// Parse current version - fail silently if dev or non-compiled
	if _, err := parseVersion(s.version); err != nil {
		return
	}

	latestVersionStr, err := getLatestVersion()
	if err != nil {
		return
	}

	cmp, err := compareVersions(latestVersionStr, s.version)
	if err != nil {
		return
	}

	if cmp == 1 {
		// New version available!
		lastSeen := fyne.CurrentApp().Preferences().StringWithFallback("LastLatestVersionSeen", "")
		if lastSeen != latestVersionStr {
			// Show popup and update preference
			showVersionPopup(latestVersionStr, s)
			fyne.CurrentApp().Preferences().SetString("LastLatestVersionSeen", latestVersionStr)
		}
	}
}

// compareVersions compares two semantic version strings using golang.org/x/mod/semver.
// Returns 1 if v1 > v2, -1 if v1 < v2, and 0 if equal.
// Returns error if parsing fails (invalid semver).
func compareVersions(v1, v2 string) (int, error) {
	v1Norm := normalize(v1)
	v2Norm := normalize(v2)

	if !semver.IsValid(v1Norm) {
		return 0, errors.New("invalid version: " + v1)
	}
	if !semver.IsValid(v2Norm) {
		return 0, errors.New("invalid version: " + v2)
	}

	return semver.Compare(v1Norm, v2Norm), nil
}
