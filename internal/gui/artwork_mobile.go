//go:build android || ios

package gui

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/storage"
	"go2tv.app/go2tv/v2/metadata"
)

func resolveMobileGUIArtwork(uri fyne.URI, mediaType string, servedMedia any) *metadata.ArtworkAsset {
	if uri == nil || !strings.HasPrefix(strings.ToLower(mediaType), "audio/") {
		return nil
	}

	if uri.Scheme() == "file" && filepath.IsAbs(uri.Path()) {
		return resolveGUIArtwork(uri.Path(), mediaType, true)
	}

	if mediaPath, ok := servedMedia.(string); ok {
		return resolveMobileEmbeddedArtwork(mediaPath, uri.Name())
	}

	reader, err := storage.ReaderSeeker(uri)
	if err != nil {
		return nil
	}
	defer reader.Close()

	file, ok := mobileUnderlyingOSFile(reader)
	if !ok {
		return nil
	}

	fdRoot := "/proc/self/fd"
	if runtime.GOOS == "ios" {
		fdRoot = "/dev/fd"
	}
	return resolveMobileEmbeddedArtwork(filepath.Join(fdRoot, strconv.FormatUint(uint64(file.Fd()), 10)), uri.Name())
}

func mobileGUIArtworkIdentity(uri fyne.URI) string {
	if uri == nil {
		return ""
	}
	return uri.String()
}

func (s *FyneScreen) resolveCurrentMobileGUIArtwork(uri fyne.URI, mediaType string, servedMedia any) *metadata.ArtworkAsset {
	identity := mobileGUIArtworkIdentity(uri)
	if identity == "" {
		s.setCurrentArtwork(nil)
		return nil
	}

	s.ensureCurrentArtworkTarget(identity)
	asset := s.cachedGUIArtwork(identity, func() *metadata.ArtworkAsset {
		return resolveMobileGUIArtwork(uri, mediaType, servedMedia)
	})
	s.setResolvedCurrentArtwork(identity, asset)
	return asset
}

func resolveMobileEmbeddedArtwork(sourcePath, mediaName string) *metadata.ArtworkAsset {
	cacheDir, err := mobileCacheDir()
	if err != nil {
		return nil
	}

	tempDir, err := os.MkdirTemp(cacheDir, "go2tv-artwork-*")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(tempDir)

	extension := filepath.Ext(mediaName)
	if extension == "" {
		return nil
	}
	mediaLink := filepath.Join(tempDir, "media"+extension)
	if err := os.Symlink(sourcePath, mediaLink); err != nil {
		return nil
	}

	asset, err := metadata.ResolveArtwork(mediaLink)
	if err != nil {
		return nil
	}
	return asset
}

func mobileUnderlyingOSFile(reader io.Reader) (*os.File, bool) {
	for reader != nil {
		switch value := reader.(type) {
		case *os.File:
			return value, true
		case interface{ Unwrap() io.ReadSeekCloser }:
			reader = value.Unwrap()
		default:
			return nil, false
		}
	}

	return nil, false
}
