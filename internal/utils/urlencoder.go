package utils

import (
	"net/url"
	"path/filepath"
	"strings"
)

// ConvertFilename is a helper function that percent-encodes a string.
func ConvertFilename(s string) string {
	out := url.QueryEscape(filepath.Base(s))
	out = strings.ReplaceAll(out, "+", "%20")
	return out
}
