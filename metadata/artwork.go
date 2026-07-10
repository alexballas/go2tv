package metadata

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cabbagekobe/tunetag"
	"golang.org/x/image/draw"
)

const (
	maxArtworkInputSize = 20 << 20
	maxArtworkPixels    = 40_000_000
	maxArtworkDimension = 600
	jpegQuality         = 88
)

var (
	ErrArtworkEmpty       = errors.New("artwork is empty")
	ErrArtworkTooLarge    = errors.New("artwork input exceeds 20 MiB")
	ErrArtworkPixels      = errors.New("artwork exceeds 40 million pixels")
	ErrArtworkUnsupported = errors.New("artwork is not JPEG or PNG")
)

var (
	sidecarExtensions = []string{".jpg", ".jpeg", ".png"}
	namedSidecars     = []string{
		"cover",
		"folder",
		"front",
		"albumart",
		"album",
		"artwork",
		"albumartlarge",
		"albumartsmall",
		"thumb",
	}
)

// Artwork describes receiver-facing artwork metadata.
type Artwork struct {
	URL      string
	MIMEType string
	Width    int
	Height   int
}

// Media describes protocol-neutral media metadata.
type Media struct {
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	Artwork     *Artwork
}

// ArtworkAsset is normalized artwork owned by the HTTP layer.
type ArtworkAsset struct {
	Data      []byte
	MIMEType  string
	Extension string
	Width     int
	Height    int
	ID        string
	Source    string
}

// HandlerPath returns the content-addressed artwork route.
func (a *ArtworkAsset) HandlerPath() string {
	return "/artwork/" + a.ID + a.Extension
}

// LoadArtwork validates and normalizes JPEG or PNG bytes.
func LoadArtwork(data []byte, source string) (*ArtworkAsset, error) {
	asset, _, err := loadArtwork(data, source)
	return asset, err
}

func loadArtwork(data []byte, source string) (*ArtworkAsset, uint64, error) {
	switch {
	case len(data) == 0:
		return nil, 0, ErrArtworkEmpty
	case len(data) > maxArtworkInputSize:
		return nil, 0, ErrArtworkTooLarge
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, fmt.Errorf("decode artwork config: %w", err)
	}
	if format != "jpeg" && format != "png" {
		return nil, 0, ErrArtworkUnsupported
	}

	pixels, err := artworkPixelArea(config.Width, config.Height)
	if err != nil {
		return nil, 0, err
	}

	sourceImage, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, fmt.Errorf("decode artwork: %w", err)
	}
	if decodedFormat != format {
		return nil, 0, ErrArtworkUnsupported
	}

	bounds := sourceImage.Bounds()
	if _, err := artworkPixelArea(bounds.Dx(), bounds.Dy()); err != nil {
		return nil, 0, err
	}

	width, height := normalizedDimensions(bounds.Dx(), bounds.Dy())
	normalized := image.NewRGBA(image.Rect(0, 0, width, height))
	stddraw.Draw(normalized, normalized.Bounds(), &image.Uniform{C: color.Black}, image.Point{}, stddraw.Src)
	if bounds.Dx() == width && bounds.Dy() == height {
		stddraw.Draw(normalized, normalized.Bounds(), sourceImage, bounds.Min, stddraw.Over)
	} else {
		draw.CatmullRom.Scale(normalized, normalized.Bounds(), sourceImage, bounds, draw.Over, nil)
	}

	var output bytes.Buffer
	if err := jpeg.Encode(&output, normalized, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, 0, fmt.Errorf("encode artwork JPEG: %w", err)
	}

	normalizedData := output.Bytes()
	sum := sha256.Sum256(normalizedData)
	return &ArtworkAsset{
		Data:      normalizedData,
		MIMEType:  "image/jpeg",
		Extension: ".jpg",
		Width:     width,
		Height:    height,
		ID:        hex.EncodeToString(sum[:]),
		Source:    source,
	}, pixels, nil
}

func artworkPixelArea(width, height int) (uint64, error) {
	if width <= 0 || height <= 0 {
		return 0, ErrArtworkUnsupported
	}
	pixels := uint64(width) * uint64(height)
	if pixels > maxArtworkPixels {
		return 0, ErrArtworkPixels
	}
	return pixels, nil
}

func normalizedDimensions(width, height int) (int, int) {
	if width <= maxArtworkDimension && height <= maxArtworkDimension {
		return width, height
	}

	scale := math.Min(
		float64(maxArtworkDimension)/float64(width),
		float64(maxArtworkDimension)/float64(height),
	)
	return max(1, int(math.Round(float64(width)*scale))),
		max(1, int(math.Round(float64(height)*scale)))
}

type sidecarCandidate struct {
	name      string
	path      string
	base      string
	extension string
}

// ResolveArtwork finds and normalizes artwork for a local media path.
func ResolveArtwork(mediaPath string) (*ArtworkAsset, error) {
	candidates, err := scanSidecars(filepath.Dir(mediaPath))
	if err != nil {
		return nil, nil
	}

	trackStem := strings.TrimSuffix(filepath.Base(mediaPath), filepath.Ext(mediaPath))
	if asset := bestSidecar(candidates, trackStem); asset != nil {
		return asset, nil
	}

	if supportsEmbeddedArtwork(mediaPath) {
		if asset := resolveEmbeddedArtwork(mediaPath); asset != nil {
			return asset, nil
		}
	}

	for _, base := range namedSidecars {
		if asset := bestSidecar(candidates, base); asset != nil {
			return asset, nil
		}
	}

	for _, size := range []string{"large", "small"} {
		for _, base := range windowsMediaPlayerBases(candidates, size) {
			if asset := bestSidecar(candidates, base); asset != nil {
				return asset, nil
			}
		}
	}

	return nil, nil
}

func scanSidecars(directory string) ([]sidecarCandidate, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("scan artwork directory: %w", err)
	}

	candidates := make([]sidecarCandidate, 0, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		extension := strings.ToLower(filepath.Ext(name))
		if !isSidecarExtension(extension) {
			continue
		}
		candidates = append(candidates, sidecarCandidate{
			name:      name,
			path:      filepath.Join(directory, name),
			base:      strings.TrimSuffix(name, filepath.Ext(name)),
			extension: extension,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		left := strings.ToLower(candidates[i].name)
		right := strings.ToLower(candidates[j].name)
		if left == right {
			return candidates[i].name < candidates[j].name
		}
		return left < right
	})
	return candidates, nil
}

func isSidecarExtension(extension string) bool {
	for _, supported := range sidecarExtensions {
		if extension == supported {
			return true
		}
	}
	return false
}

func bestSidecar(candidates []sidecarCandidate, base string) *ArtworkAsset {
	var (
		best          *ArtworkAsset
		bestArea      uint64
		bestExtension = len(sidecarExtensions)
	)

	for _, candidate := range candidates {
		if !strings.EqualFold(candidate.base, base) {
			continue
		}
		data, err := readArtworkFile(candidate.path)
		if err != nil {
			continue
		}
		asset, area, err := loadArtwork(data, candidate.path)
		if err != nil {
			continue
		}
		extension := sidecarExtensionRank(candidate.extension)
		if best == nil || area > bestArea || area == bestArea && extension < bestExtension {
			best = asset
			bestArea = area
			bestExtension = extension
		}
	}
	return best
}

func readArtworkFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxArtworkInputSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxArtworkInputSize {
		return nil, ErrArtworkTooLarge
	}
	return data, nil
}

func sidecarExtensionRank(extension string) int {
	for i, supported := range sidecarExtensions {
		if extension == supported {
			return i
		}
	}
	return len(sidecarExtensions)
}

func supportsEmbeddedArtwork(mediaPath string) bool {
	switch strings.ToLower(filepath.Ext(mediaPath)) {
	case ".mp3", ".m4a", ".mp4", ".flac", ".ogg", ".oga", ".opus":
		return true
	default:
		return false
	}
}

func resolveEmbeddedArtwork(mediaPath string) *ArtworkAsset {
	tag, err := tunetag.Open(mediaPath)
	if err != nil {
		return nil
	}
	return loadEmbeddedPictures(tag.Pictures(), mediaPath)
}

func loadEmbeddedPictures(pictures []tunetag.Picture, source string) *ArtworkAsset {
	for _, picture := range pictures {
		if picture.Type != tunetag.PictureCoverFront {
			continue
		}
		asset, err := LoadArtwork(picture.Data, source)
		if err == nil {
			return asset
		}
	}
	for _, picture := range pictures {
		if picture.Type == tunetag.PictureCoverFront {
			continue
		}
		asset, err := LoadArtwork(picture.Data, source)
		if err == nil {
			return asset
		}
	}
	return nil
}

func windowsMediaPlayerBases(candidates []sidecarCandidate, size string) []string {
	var bases []string
	for _, candidate := range candidates {
		lower := strings.ToLower(candidate.base)
		prefix := "albumart_"
		suffix := "_" + size
		if !strings.HasPrefix(lower, prefix) || !strings.HasSuffix(lower, suffix) {
			continue
		}
		if len(lower) == len(prefix)+len(suffix) {
			continue
		}
		found := false
		for _, base := range bases {
			if strings.EqualFold(base, candidate.base) {
				found = true
				break
			}
		}
		if !found {
			bases = append(bases, candidate.base)
		}
	}
	return bases
}
