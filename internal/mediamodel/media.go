package mediamodel

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"slices"
	"strings"
)

type MediaKind string

const (
	MediaKindUnknown MediaKind = ""
	MediaKindImage   MediaKind = "image"
	MediaKindVideo   MediaKind = "video"
	MediaKindAudio   MediaKind = "audio"
)

var (
	imageExtensions = [...]string{".jpg", ".jpeg", ".png"}
	videoExtensions = [...]string{".mp4", ".avi", ".mkv", ".mpeg", ".mov", ".webm", ".m4v", ".mpv", ".dv"}
	audioExtensions = [...]string{".mp3", ".flac", ".wav", ".m4a"}
	srtExtensions   = [...]string{".srt"}
	vttExtensions   = [...]string{".vtt"}
)

func ImageExtensions() []string { return slices.Clone(imageExtensions[:]) }
func VideoExtensions() []string { return slices.Clone(videoExtensions[:]) }
func AudioExtensions() []string { return slices.Clone(audioExtensions[:]) }
func SRTExtensions() []string   { return slices.Clone(srtExtensions[:]) }
func VTTExtensions() []string   { return slices.Clone(vttExtensions[:]) }

func AllMediaExtensions() []string {
	extensions := make([]string, 0, len(imageExtensions)+len(videoExtensions)+len(audioExtensions))
	extensions = append(extensions, videoExtensions[:]...)
	extensions = append(extensions, audioExtensions[:]...)
	extensions = append(extensions, imageExtensions[:]...)
	return extensions
}

func IsImageExtension(extension string) bool { return containsExtension(imageExtensions[:], extension) }
func IsVideoExtension(extension string) bool { return containsExtension(videoExtensions[:], extension) }
func IsAudioExtension(extension string) bool { return containsExtension(audioExtensions[:], extension) }
func IsSRTPath(path string) bool             { return containsExtension(srtExtensions[:], filepath.Ext(path)) }
func IsVTTPath(path string) bool             { return containsExtension(vttExtensions[:], filepath.Ext(path)) }

func containsExtension(extensions []string, extension string) bool {
	return slices.Contains(extensions, strings.ToLower(extension))
}

func KindForPath(path string) MediaKind {
	extension := filepath.Ext(path)
	switch {
	case IsImageExtension(extension):
		return MediaKindImage
	case IsVideoExtension(extension):
		return MediaKindVideo
	case IsAudioExtension(extension):
		return MediaKindAudio
	default:
		return MediaKindUnknown
	}
}

type QueueItem struct {
	id           string
	source       string
	path         string
	displayPath  string
	baseName     string
	parentFolder string
	mediaKind    MediaKind
}

func NewQueueItem(source string) (QueueItem, bool) {
	path, err := filepath.Abs(source)
	if err != nil {
		return QueueItem{}, false
	}

	kind := KindForPath(path)
	if kind == MediaKindUnknown {
		return QueueItem{}, false
	}

	displayPath := QueueDisplayPath(path)
	return QueueItem{
		id:           randomID(),
		source:       source,
		path:         path,
		displayPath:  displayPath,
		baseName:     filepath.Base(displayPath),
		parentFolder: filepath.Dir(displayPath),
		mediaKind:    kind,
	}, true
}

// NewQueueReference builds display-only queue metadata for an opaque source.
// The source is never interpreted as a filesystem path.
func NewQueueReference(source, name, parent string, kind MediaKind) (QueueItem, bool) {
	if source == "" || name == "" || kind == MediaKindUnknown {
		return QueueItem{}, false
	}
	return QueueItem{id: randomID(), source: source, displayPath: name, baseName: name, parentFolder: parent, mediaKind: kind}, true
}

func BuildQueueItems(paths []string) []QueueItem {
	items := make([]QueueItem, 0, len(paths))
	for _, path := range SortedMediaPaths(paths) {
		item, ok := NewQueueItem(path)
		if ok {
			items = append(items, item)
		}
	}
	return items
}

func (item QueueItem) ID() string           { return item.id }
func (item QueueItem) Source() string       { return item.source }
func (item QueueItem) Path() string         { return item.path }
func (item QueueItem) DisplayPath() string  { return item.displayPath }
func (item QueueItem) BaseName() string     { return item.baseName }
func (item QueueItem) ParentFolder() string { return item.parentFolder }
func (item QueueItem) MediaKind() MediaKind { return item.mediaKind }

func randomID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("mediamodel: random ID: " + err.Error())
	}
	return hex.EncodeToString(value[:])
}

func SortedMediaPaths(paths []string) []string {
	sorted := slices.Clone(paths)
	slices.SortFunc(sorted, CompareMediaPaths)
	return sorted
}

func CompareMediaPaths(a, b string) int {
	aName := strings.ToLower(filepath.Base(filepath.Clean(a)))
	bName := strings.ToLower(filepath.Base(filepath.Clean(b)))
	if cmp := strings.Compare(aName, bName); cmp != 0 {
		return cmp
	}

	aPath := strings.ToLower(filepath.Clean(a))
	bPath := strings.ToLower(filepath.Clean(b))
	if cmp := strings.Compare(aPath, bPath); cmp != 0 {
		return cmp
	}

	return strings.Compare(filepath.Clean(a), filepath.Clean(b))
}
