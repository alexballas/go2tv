package mediaartwork

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"go2tv.app/go2tv/v2/internal/mediamodel"
)

func TestThumbnailReusesGUICompatibleCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mediaPath := filepath.Join(t.TempDir(), "picture.png")
	writeArtworkImage(t, mediaPath, "png", 240, 120)
	request := Request{Path: mediaPath, Kind: mediamodel.MediaKindImage}
	cachePath, err := thumbnailCachePath(request)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	var cached bytes.Buffer
	if err = jpeg.Encode(&cached, solidArtworkImage(thumbnailSize, thumbnailSize), &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(cachePath, cached.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := Thumbnail(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, cached.Bytes()) {
		t.Fatal("existing GUI thumbnail cache not reused")
	}
}

func TestImageArtworkAndThumbnailDimensions(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	mediaPath := filepath.Join(t.TempDir(), "picture.png")
	writeArtworkImage(t, mediaPath, "png", 1200, 600)
	request := Request{Path: mediaPath, Kind: mediamodel.MediaKindImage}

	asset, err := Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if asset.Width != 600 || asset.Height != 300 {
		t.Fatalf("artwork = %dx%d", asset.Width, asset.Height)
	}
	data, err := Thumbnail(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" || config.Width != thumbnailSize || config.Height != thumbnailSize {
		t.Fatalf("thumbnail = %s %#v, err=%v", format, config, err)
	}
}

func writeArtworkImage(t *testing.T, path, format string, width, height int) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if format == "png" {
		err = png.Encode(file, solidArtworkImage(width, height))
	} else {
		err = jpeg.Encode(file, solidArtworkImage(width, height), nil)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
}

func solidArtworkImage(width, height int) image.Image {
	result := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			result.Set(x, y, color.RGBA{R: 40, G: 120, B: 210, A: 255})
		}
	}
	return result
}
