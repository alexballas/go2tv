package gui

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/widget"
	"golang.org/x/mod/semver"
)

const disableVersionNotificationsPref = "DisableVersionNotifications"

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

// validVersion reports whether version is semver (false for "dev" builds).
func validVersion(version string) bool {
	return semver.IsValid(normalize(version))
}

// silentCheckVersion performs a background version check and notifies the user
// if a new version is available, but only once per version.
func silentCheckVersion(s *FyneScreen) {
	if fyne.CurrentApp().Preferences().BoolWithFallback(disableVersionNotificationsPref, false) {
		return
	}

	// Fail silently for dev or non-compiled builds.
	if !validVersion(s.version) {
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

func showVersionPopup(latestVersionStr string, s *FyneScreen) {
	fyne.Do(func() {
		lbl := widget.NewLabel(lang.L("Current") + ": v" + s.version + " → " + lang.L("New") + ": " + latestVersionStr)
		lbl.Alignment = fyne.TextAlignCenter
		cnf := dialog.NewCustomConfirm(
			lang.L("Version checker"),
			lang.L("Download"),
			lang.L("Cancel"),
			lbl,
			func(b bool) {
				if b {
					u, _ := url.Parse("https://go2tv.app/latest")
					_ = fyne.CurrentApp().OpenURL(u)
				}
			},
			s.Current,
		)
		cnf.Show()
	})
}
