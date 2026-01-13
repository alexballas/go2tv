package gui

import (
	"errors"
	"strings"

	"golang.org/x/mod/semver"
)

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
