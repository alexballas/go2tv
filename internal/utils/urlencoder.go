package utils

import (
	"net/url"
	"path"
	"strings"
)

// ConvertFilename is a helper function that percent-encodes a string.
func ConvertFilename(s string) string {
	out := url.QueryEscape(path.Base(s))
	out = strings.ReplaceAll(out, "+", "%20")
	return out
}
