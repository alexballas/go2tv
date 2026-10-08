package torrentstream

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"go2tv.app/go2tv/v2/internal/mediasource"
)

func TestTorrentMediaSelection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "collection")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"00-readme.txt", "01-movie.mp4", "02-movie.mkv"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(root); err != nil {
		t.Fatal(err)
	}
	encoded, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	session, err := openWithConfig(context.Background(), "", &metainfo.MetaInfo{InfoBytes: encoded}, localConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	files, err := session.Files(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Index != 1 || files[1].Index != 2 {
		t.Fatalf("media choices = %v", files)
	}
	if _, err := session.SelectMedia(context.Background(), -1); err == nil || !strings.Contains(err.Error(), "1: 01-movie.mp4") || !strings.Contains(err.Error(), "2: 02-movie.mkv") {
		t.Fatalf("ambiguous media must list original indexes: %v", err)
	}
	tt := []struct {
		name  string
		index int
		valid bool
	}{
		{name: "non-media", index: 0},
		{name: "selected movie", index: 2, valid: true},
		{name: "out of range", index: 3},
		{name: "invalid negative", index: -2},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			path, err := session.SelectMedia(context.Background(), tc.index)
			if !tc.valid {
				if err == nil {
					t.Fatal("invalid selection accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			source, ok := mediasource.Lookup(path)
			if !ok || source.MIME() != "video/x-matroska" || filepath.Base(path) != "02-movie.mkv" {
				t.Fatalf("selected wrong file: %q", path)
			}
		})
	}
}

func TestMagnetMetadataCancellation(t *testing.T) {
	tt := []struct{ name, magnet string }{
		{name: "v1", magnet: "magnet:?xt=urn:btih:0123456789012345678901234567890123456789"},
		{name: "v2 only", magnet: "magnet:?xt=urn:btmh:12200123456789012345678901234567890123456789012345678901234567890123"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) { testMagnetMetadataCancellation(t, tc.magnet) })
	}
}

func testMagnetMetadataCancellation(t *testing.T, magnet string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	session, err := openWithConfig(ctx, magnet, nil, localConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result := make(chan error, 1)
	go func() { _, err := session.Files(ctx); result <- err }()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("metadata cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("metadata fetch ignored cancellation")
	}
}

func TestInvalidTorrentInput(t *testing.T) {
	tt := []struct {
		name string
		open func() (*Session, error)
	}{
		{name: "invalid magnet", open: func() (*Session, error) { return Open(context.Background(), "magnet:?xt=invalid") }},
		{name: "invalid document", open: func() (*Session, error) { return OpenReader(context.Background(), strings.NewReader("not a torrent")) }},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			session, err := tc.open()
			if session != nil {
				defer session.Close()
			}
			if err == nil {
				t.Fatal("invalid torrent accepted")
			}
		})
	}
}
