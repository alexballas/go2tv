package castsubtitles

import (
	"path/filepath"
	"strings"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/mkvsubs"
)

// RegisterTorrent exposes progressive embedded text only for registered torrent
// sources. A false automatic value removes stale captions.
func RegisterTorrent(server *httphandlers.HTTPserver, host, path string, automatic bool, offset int, transcoded bool) string {
	if server == nil {
		return ""
	}
	const endpoint = "/torrent-subtitles.json"
	server.RemoveHandler(endpoint)
	if host == "" {
		return ""
	}
	source := TorrentSource(path, automatic)
	if source == nil {
		return ""
	}
	server.AddHandler(endpoint, nil, nil, mkvsubs.Handler(mkvsubs.New(source), float64(offset), transcoded))
	return "http://" + host + endpoint
}

// TorrentSource selects embedded text captions only for registered Matroska
// torrents. The caller decides whether to burn them or use receiver captions.
func TorrentSource(path string, automatic bool) mediasource.Source {
	if !automatic {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".mkv" && ext != ".webm" {
		return nil
	}
	source, ok := mediasource.Lookup(path)
	if !ok {
		return nil
	}
	return source
}
