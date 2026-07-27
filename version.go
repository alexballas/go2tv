package go2tv

import (
	_ "embed"
	"strings"
)

//go:embed version.txt
var version string

// Version returns the application version embedded from version.txt.
func Version() string {
	return strings.TrimSpace(version)
}
