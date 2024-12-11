//go:build !android

package locale

import (
	"strings"
)

// SetRunOnJVM is a noop, this function is only valid on Android
func SetRunOnJVM(_ func(fn func(vm, env, ctx uintptr) error) error) {}

func splitLocale(locale string) (string, string) {
	// Remove the encoding, if present.
	formattedLocale, _, _ := strings.Cut(locale, ".")

	// Normalize by replacing the hyphens with underscores
	formattedLocale = strings.ReplaceAll(formattedLocale, "-", "_")

	// Split at the underscore.
	language, territory, _ := strings.Cut(formattedLocale, "_")
	return language, territory
}
