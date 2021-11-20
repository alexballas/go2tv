package utils

import (
	"net/url"
	"strings"
)

// ConvertFilename is a helper function that percent-encodes a string.
func ConvertFilename(s string) string {
	out := url.QueryEscape(base(s))
	out = strings.ReplaceAll(out, "+", "%20")
	return out
}

func base(path string) string {
	if path == "" {
		return "."
	}
	// Strip trailing slashes.
	for len(path) > 0 && isURLSeparator(path[len(path)-1]) {
		path = path[0 : len(path)-1]
	}

	// Find the last element
	i := len(path) - 1
	for i >= 0 && !isURLSeparator(path[i]) {
		i--
	}
	if i >= 0 {
		path = path[i+1:]
	}
	// If empty now, it had only slashes.
	if path == "" {
		return "/"
	}
	return path
}

func isURLSeparator(c uint8) bool {
	return c == '/'
}
