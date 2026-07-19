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
