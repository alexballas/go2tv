package mediaartwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/metadata"
	"go2tv.app/go2tv/v2/utils"
	"golang.org/x/image/draw"
)

const (
	thumbnailSize  = 128
	thumbnailLimit = 20 << 20
	maxCacheSize   = 500 * 1024 * 1024
	maxCacheFiles  = 20000
)

var (
	cleanupOnce  sync.Once
	resolveSlots = make(chan struct{}, 4)
)

type AudioResolver func() (*metadata.ArtworkAsset, error)
type Resolver func(context.Context) (*metadata.ArtworkAsset, error)

// Request describes one local media source. File remains caller-owned.
type Request struct {
	Path         string
	Kind         mediamodel.MediaKind
	FFmpegPath   string
	File         *os.File
	ResolveAudio AudioResolver
}

// Resolve returns normalized receiver/UI artwork for audio, images, or video.
func Resolve(ctx context.Context, request Request) (*metadata.ArtworkAsset, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case resolveSlots <- struct{}{}:
		defer func() { <-resolveSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	switch request.Kind {
	case mediamodel.MediaKindAudio:
		if request.ResolveAudio != nil {
			return request.ResolveAudio()
		}
		return metadata.ResolveArtwork(request.Path)
	case mediamodel.MediaKindImage:
		return resolveImage(request)
	case mediamodel.MediaKindVideo:
		return resolveVideo(ctx, request)
	default:
		return nil, nil
	}
}

// Thumbnail reads/writes the GUI-compatible persistent 128px JPEG cache.
func Thumbnail(ctx context.Context, request Request) ([]byte, error) {
	return ThumbnailWithResolver(ctx, request, func(ctx context.Context) (*metadata.ArtworkAsset, error) {
		return Resolve(ctx, request)
	})
}

// ThumbnailWithResolver preserves cache reuse while letting callers share
// their full-artwork cache on a miss.
func ThumbnailWithResolver(ctx context.Context, request Request, resolve Resolver) ([]byte, error) {
	cachePath, err := thumbnailCachePath(request)
	if err == nil {
		if data, readErr := readCachedThumbnail(cachePath); readErr == nil {
			markAccessed(cachePath)
			return data, nil
		}
	}

	if resolve == nil {
		return nil, errors.New("artwork resolver required")
	}
	asset, err := resolve(ctx)
	if err != nil || asset == nil {
		return nil, err
	}
	source, err := jpeg.Decode(bytes.NewReader(asset.Data))
	if err != nil {
		return nil, fmt.Errorf("decode normalized artwork: %w", err)
	}
	data, err := encodeThumbnail(source)
	if err != nil {
		return nil, err
	}
	if cachePath != "" {
		if err = writeCachedThumbnail(cachePath, data); err == nil {
			cleanupOnce.Do(func() { go cleanupCache(filepath.Dir(cachePath)) })
		}
	}
	return data, nil
}

// CacheID returns a source-fingerprinted key for normalized full artwork.
func CacheID(request Request) (string, error) {
	key, err := thumbnailCacheKey(request)
	if err != nil {
		return "", err
	}
	return "media-artwork-v1:" + key, nil
}

// AssetFromJPEG restores a normalized cached asset without re-encoding it.
func AssetFromJPEG(data []byte, source string) (*metadata.ArtworkAsset, error) {
	if len(data) == 0 {
		return nil, metadata.ErrArtworkEmpty
	}
	if len(data) > thumbnailLimit {
		return nil, metadata.ErrArtworkTooLarge
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if format != "jpeg" || config.Width <= 0 || config.Height <= 0 {
		return nil, metadata.ErrArtworkUnsupported
	}
	copyData := append([]byte(nil), data...)
	sum := sha256.Sum256(copyData)
	return &metadata.ArtworkAsset{
		Data:      copyData,
		MIMEType:  "image/jpeg",
		Extension: ".jpg",
		Width:     config.Width,
		Height:    config.Height,
		ID:        hex.EncodeToString(sum[:]),
		Source:    source,
	}, nil
}

func resolveImage(request Request) (*metadata.ArtworkAsset, error) {
	file, closeFile, err := requestFile(request)
	if err != nil {
		return nil, err
	}
	if closeFile {
		defer file.Close()
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, thumbnailLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > thumbnailLimit {
		return nil, metadata.ErrArtworkTooLarge
	}
	return metadata.LoadArtwork(data, request.Path)
}

func resolveVideo(ctx context.Context, request Request) (*metadata.ArtworkAsset, error) {
	input := any(request.Path)
	if request.File != nil {
		input = request.File
	}
	data, err := utils.ExtractVideoThumbnail(ctx, request.FFmpegPath, input)
	if err == nil {
		return metadata.LoadArtwork(data, request.Path)
	}
	if strings.EqualFold(filepath.Ext(request.Path), ".mp4") {
		if request.ResolveAudio != nil {
			return request.ResolveAudio()
		}
		if request.File != nil {
			return metadata.ResolveEmbeddedArtwork(request.File, request.Path)
		}
		return metadata.ResolveArtwork(request.Path)
	}
	return nil, err
}

func requestFile(request Request) (*os.File, bool, error) {
	if request.File != nil {
		return request.File, false, nil
	}
	file, err := os.Open(request.Path)
	return file, true, err
}

func encodeThumbnail(source image.Image) ([]byte, error) {
	destination := image.NewRGBA(image.Rect(0, 0, thumbnailSize, thumbnailSize))
	stddraw.Draw(destination, destination.Bounds(), &image.Uniform{C: color.Black}, image.Point{}, stddraw.Src)
	bounds := source.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, metadata.ErrArtworkUnsupported
	}
	ratio := float64(bounds.Dx()) / float64(bounds.Dy())
	width, height := thumbnailSize, thumbnailSize
	if ratio > 1 {
		height = max(1, int(float64(thumbnailSize)/ratio))
	} else {
		width = max(1, int(float64(thumbnailSize)*ratio))
	}
	x := (thumbnailSize - width) / 2
	y := (thumbnailSize - height) / 2
	draw.ApproxBiLinear.Scale(destination, image.Rect(x, y, x+width, y+height), source, bounds, draw.Over, nil)

	var output bytes.Buffer
	if err := jpeg.Encode(&output, destination, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func thumbnailCachePath(request Request) (string, error) {
	directory, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	key, err := thumbnailCacheKey(request)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "xfilepicker", key+".jpg"), nil
}

func thumbnailCacheKey(request Request) (string, error) {
	path, err := filepath.Abs(request.Path)
	if err != nil {
		path = request.Path
	}
	file, closeFile, err := requestFile(request)
	if err != nil {
		return "", err
	}
	if closeFile {
		defer file.Close()
	}
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(path))
	_, _ = hash.Write([]byte(info.ModTime().String()))
	_, _ = hash.Write([]byte(fmt.Sprintf("%d", info.Size())))
	if request.Kind == mediamodel.MediaKindAudio || strings.EqualFold(filepath.Ext(path), ".mp4") {
		addSidecarState(hash, filepath.Dir(path))
	}
	if _, err = file.Seek(0, io.SeekStart); err == nil {
		_, _ = io.CopyN(hash, file, 32*1024)
		_, _ = file.Seek(0, io.SeekStart)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func addSidecarState(writer io.Writer, directory string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".jpg", ".jpeg", ".png":
		default:
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(writer, "%s\x00%s\x00%d\x00", entry.Name(), info.ModTime().String(), info.Size())
	}
}

func readCachedThumbnail(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, thumbnailLimit+1))
	if err != nil || len(data) == 0 || len(data) > thumbnailLimit {
		return nil, errors.New("invalid cached thumbnail")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || config.Width != thumbnailSize || config.Height != thumbnailSize {
		return nil, errors.New("invalid cached thumbnail")
	}
	return data, nil
}

func writeCachedThumbnail(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".go2tv-thumbnail-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err = temp.Write(data); err == nil {
		err = temp.Close()
	} else {
		_ = temp.Close()
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tempPath, path); err != nil {
		return err
	}
	markAccessed(path)
	return nil
}

func markAccessed(path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

func cleanupCache(directory string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	type cacheFile struct {
		path string
		size int64
		time time.Time
	}
	files := make([]cacheFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jpg" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, cacheFile{path: filepath.Join(directory, entry.Name()), size: info.Size(), time: info.ModTime()})
		total += info.Size()
	}
	if total <= maxCacheSize && len(files) <= maxCacheFiles {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].time.Before(files[j].time) })
	for len(files) > 0 && (total > maxCacheSize*8/10 || len(files) > maxCacheFiles*8/10) {
		_ = os.Remove(files[0].path)
		total -= files[0].size
		files = files[1:]
	}
}
