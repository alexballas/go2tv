package gui

import (
	"path/filepath"
	"strings"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/mkvsubs"
)

// Local files retain their existing extraction path. Only registered torrent
// sources can expose progressive embedded text to our custom receiver.
func registerTorrentSubtitles(server *httphandlers.HTTPserver, host, path string, automatic bool, offset int) string {
	if server == nil {
		return ""
	}
	const endpoint = "/torrent-subtitles.json"
	server.RemoveHandler(endpoint)
	if !automatic || host == "" {
		return ""
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".mkv" && ext != ".webm" {
		return ""
	}
	source, ok := mediasource.Lookup(path)
	if !ok {
		return ""
	}
	server.AddHandler(endpoint, nil, nil, mkvsubs.Handler(mkvsubs.New(source), float64(offset)))
	return "http://" + host + endpoint
}
