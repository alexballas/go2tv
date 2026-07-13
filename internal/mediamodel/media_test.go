package mediamodel

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestBuildQueueItemsDuplicatesStableIDsAndMixedCase(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Movie.MKv")
	items := BuildQueueItems([]string{video, video, filepath.Join(dir, "ignored.txt")})
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].Path() != video || items[1].Path() != video {
		t.Fatalf("duplicate paths not preserved: %q, %q", items[0].Path(), items[1].Path())
	}
	if items[0].MediaKind() != MediaKindVideo {
		t.Fatalf("kind = %q, want video", items[0].MediaKind())
	}
	if items[0].ID() == "" || items[1].ID() == "" || items[0].ID() == items[1].ID() {
		t.Fatalf("IDs not unique: %q, %q", items[0].ID(), items[1].ID())
	}
	queue := NewQueue(items, 0)
	clone := queue.Clone()
	for index, item := range clone.Items() {
		if item.ID() != items[index].ID() {
			t.Fatalf("clone ID %d changed", index)
		}
	}
}

func TestExtensionSnapshotsCannotMutateDefaults(t *testing.T) {
	images := ImageExtensions()
	images[0] = ".bad"
	all := AllMediaExtensions()
	all[0] = ".bad"
	if !IsImageExtension(".JPG") || KindForPath("photo.JpEg") != MediaKindImage {
		t.Fatal("image defaults mutated or mixed-case failed")
	}
	if !IsVideoExtension(".MKV") || !IsAudioExtension(".Mp3") {
		t.Fatal("mixed-case membership failed")
	}
	if !IsSRTPath("captions.SrT") || !IsVTTPath("captions.VtT") {
		t.Fatal("subtitle mixed-case membership failed")
	}
}

func TestSortedMediaPaths(t *testing.T) {
	paths := []string{"/z/02.mp4", "/b/01.mp4", "/a/01.mp4", "/a/01.MP4"}
	got := SortedMediaPaths(paths)
	want := []string{"/a/01.MP4", "/a/01.mp4", "/b/01.mp4", "/z/02.mp4"}
	if !slices.Equal(got, want) {
		t.Fatalf("sorted = %v, want %v", got, want)
	}
	if paths[0] != "/z/02.mp4" {
		t.Fatal("input mutated")
	}
}

func TestNewQueueItemAccessors(t *testing.T) {
	source := filepath.Join(t.TempDir(), "song.mp3")
	item, ok := NewQueueItem(source)
	if !ok {
		t.Fatal("NewQueueItem failed")
	}
	if item.Source() != source || item.Path() != source || item.DisplayPath() != source {
		t.Fatalf("paths = source %q path %q display %q", item.Source(), item.Path(), item.DisplayPath())
	}
	if item.BaseName() != "song.mp3" || item.ParentFolder() != filepath.Dir(source) {
		t.Fatalf("display metadata = %q, %q", item.BaseName(), item.ParentFolder())
	}
}
