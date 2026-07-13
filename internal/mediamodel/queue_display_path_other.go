//go:build !linux || android

package mediamodel

func QueueDisplayPath(path string) string { return path }
