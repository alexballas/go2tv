package utils

import (
	"os"

	"go2tv.app/go2tv/v2/internal/mediasource"
)

// mediaInput resolves progressive sources before any filesystem access.
func mediaInput(path string) (string, error) {
	if source, ok := mediasource.Lookup(path); ok {
		return source.URL(), nil
	}
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return path, nil
}
