//go:build android || ios

package gui

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/storage"
	"go2tv.app/go2tv/v2/metadata"
)

func resolveMobileGUIArtwork(uri fyne.URI, mediaType string, servedMedia any) *metadata.ArtworkAsset {
	if uri == nil || !strings.HasPrefix(strings.ToLower(mediaType), "audio/") {
		return nil
	}

	// A real path is the only case that can also see sidecar images sitting
	// next to the track. It is not a given that we may read it - an iOS pick
	// resolves to a path outside our sandbox - so fall through on failure.
	if uri.Scheme() == "file" && filepath.IsAbs(uri.Path()) {
		if asset := resolveGUIArtwork(uri.Path(), mediaType, true); asset != nil {
			return asset
		}
	}

	// Media we already copied into our own cache.
	if mediaPath, ok := servedMedia.(string); ok {
		if asset := resolveMobileEmbeddedArtwork(mediaPath, uri.Name()); asset != nil {
			return asset
		}
	}

	// content:// documents and sandboxed iOS picks: read the tags off the
	// descriptor we were handed. Reopening it by path - /proc/self/fd on
	// Android, /dev/fd on iOS - re-runs the permission check against the
	// underlying file, which scoped storage and the iOS sandbox deny even
	// though the descriptor itself is perfectly readable.
	reader, err := storage.ReaderSeeker(uri)
	if err != nil {
		return nil
	}
	defer reader.Close()

	file, ok := mobileUnderlyingOSFile(reader)
	if !ok {
		return nil
	}

	asset, err := metadata.ResolveEmbeddedArtwork(file, uri.Name())
	if err != nil {
		return nil
	}
	return asset
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

// resolveMobileEmbeddedArtwork reads embedded artwork out of a file we hold a
// path to. mediaName, not sourcePath, carries the extension that decides which
// container to parse: the cache copy is named after a temp pattern.
func resolveMobileEmbeddedArtwork(sourcePath, mediaName string) *metadata.ArtworkAsset {
	file, err := os.Open(sourcePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	asset, err := metadata.ResolveEmbeddedArtwork(file, mediaName)
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
