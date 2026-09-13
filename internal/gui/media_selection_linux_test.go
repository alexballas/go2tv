//go:build linux && !android

package gui

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/storage"
	"golang.org/x/sys/unix"
)

func TestMediaCardPortalPaths(t *testing.T) {
	s, c := newMediaCardTestScreen(t)
	c.subtitles.SetSelected(lang.L(subtitleExternal))
	tt := []struct {
		name string
		row  *selectionRow
	}{
		{"movie.mp4", c.media},
		{"subtitle.srt", c.subs},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name)
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			hostPath := "/home/user/" + tc.name
			if err := unix.Setxattr(path, "user.document-portal.host-path", []byte(hostPath), 0); err != nil {
				if errors.Is(err, unix.ENOTSUP) {
					t.Skipf("xattrs unsupported: %v", err)
				}
				t.Fatal(err)
			}
			if tc.row == c.media {
				if err := setCurrentMediaPath(s, path); err != nil {
					t.Fatal(err)
				}
				if s.mediafile != path {
					t.Fatal("playback must retain portal path")
				}
			} else {
				selectSubsFile(s, storage.NewFileURI(path))
				if s.subsfile != path {
					t.Fatal("subtitles must retain portal path")
				}
			}
			if tc.row.path.Text != hostPath || tc.row.name.Text != tc.name {
				t.Fatalf("display = %q, %q; want %q, %q", tc.row.name.Text, tc.row.path.Text, tc.name, hostPath)
			}
		})
	}
}
