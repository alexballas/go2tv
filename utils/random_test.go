package utils

import (
	"encoding/hex"
	"testing"
)

// RandomString produces the DLNA callback path, so the properties that matter
// are that it is a fixed-width, URL-safe token that never repeats. A failure to
// generate is not reachable: crypto/rand.Read panics rather than returning an
// error, so asserting on err alone tests nothing.
func TestRandomString(t *testing.T) {
	const wantLen = 32 // 16 bytes rendered as hex

	seen := make(map[string]struct{}, 64)
	for range 64 {
		got, err := RandomString()
		if err != nil {
			t.Fatalf("RandomString() error = %v", err)
		}
		if len(got) != wantLen {
			t.Fatalf("RandomString() = %q, want %d chars", got, wantLen)
		}
		if _, err := hex.DecodeString(got); err != nil {
			t.Fatalf("RandomString() = %q, want URL-safe hex: %v", got, err)
		}
		if _, dup := seen[got]; dup {
			t.Fatalf("RandomString() repeated %q; callback paths must be unguessable", got)
		}
		seen[got] = struct{}{}
	}
}
