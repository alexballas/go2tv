//go:build windows

package library

import "testing"

func TestDriveRootsKeepDistinctDisplayNames(t *testing.T) {
	for _, root := range []string{`C:\`, `X:\`} {
		if got := rootDisplayName(root); got != root {
			t.Fatalf("rootDisplayName(%q) = %q", root, got)
		}
	}
}

func TestDuplicateRootNamesUseDriveContext(t *testing.T) {
	want := []string{`Movies - C:`, `Movies - X:`}
	got := rootDisplayNames([]string{`C:\Media\Movies`, `X:\Media\Movies`})
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("rootDisplayNames() = %q, want %q", got, want)
		}
	}
}
