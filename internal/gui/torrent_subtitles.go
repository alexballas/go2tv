package gui

import (
	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/castsubtitles"
)

func registerTorrentSubtitles(server *httphandlers.HTTPserver, host, path string, automatic bool, offset int) string {
	return castsubtitles.RegisterTorrent(server, host, path, automatic, offset)
}
