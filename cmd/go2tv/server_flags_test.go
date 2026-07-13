package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go2tv.app/go2tv/v2/internal/servermode"
)

func TestServerFlagWiring(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		server    bool
		listenSet bool
		roots     []string
		origins   []string
		legacy    []string
		args      []string
		want      error
	}{
		{name: "server default listen", server: true, roots: []string{"root"}},
		{name: "server explicit listen", server: true, listenSet: true, roots: []string{"root"}},
		{name: "server repeatable roots origins", server: true, listenSet: true, roots: []string{"one", "two"}, origins: []string{"http://one.test:9666", "http://two.test:9666"}},
		{name: "server requires root", server: true, want: servermode.ErrInvalidMediaRoot},
		{name: "conflict version", server: true, roots: []string{"root"}, legacy: []string{"-version"}, want: servermode.ErrServerFlagConflict},
		{name: "conflict video", server: true, roots: []string{"root"}, legacy: []string{"-v"}, want: servermode.ErrServerFlagConflict},
		{name: "conflict URL", server: true, roots: []string{"root"}, legacy: []string{"-u"}, want: servermode.ErrServerFlagConflict},
		{name: "conflict subtitles", server: true, roots: []string{"root"}, legacy: []string{"-s"}, want: servermode.ErrServerFlagConflict},
		{name: "conflict target", server: true, roots: []string{"root"}, legacy: []string{"-t"}, want: servermode.ErrServerFlagConflict},
		{name: "conflict transcode", server: true, roots: []string{"root"}, legacy: []string{"-tc"}, want: servermode.ErrServerFlagConflict},
		{name: "conflict list", server: true, roots: []string{"root"}, legacy: []string{"-l"}, want: servermode.ErrServerFlagConflict},
		{name: "listen without server", listenSet: true, want: servermode.ErrServerFlagWithoutMode},
		{name: "root without server", roots: []string{"root"}, want: servermode.ErrServerFlagWithoutMode},
		{name: "origin without server", origins: []string{"http://one.test:9666"}, want: servermode.ErrServerFlagWithoutMode},
		{name: "positional without server", args: []string{"file"}, want: servermode.ErrPositionalArguments},
		{name: "positional with server", server: true, roots: []string{"root"}, args: []string{"file"}, want: servermode.ErrPositionalArguments},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateServerFlagValues(tt.server, tt.listenSet, tt.roots, tt.origins, tt.legacy, tt.args)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestServerConfigWiring(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	other := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		cfg  servermode.Config
		want error
	}{
		{name: "default listen", cfg: servermode.Config{MediaRoots: []string{root}}},
		{name: "explicit listen", cfg: servermode.Config{Listen: "127.0.0.1:0", MediaRoots: []string{root}}},
		{name: "repeatable roots origins", cfg: servermode.Config{Listen: "0.0.0.0:9666", MediaRoots: []string{root, other}, AllowedOrigins: []string{"http://192.0.2.1:9666", "http://media.test:9666"}}},
		{name: "invalid listen", cfg: servermode.Config{Listen: "bad", MediaRoots: []string{root}}, want: servermode.ErrInvalidListen},
		{name: "nonloopback requires origin", cfg: servermode.Config{Listen: "0.0.0.0:9666", MediaRoots: []string{root}}, want: servermode.ErrAllowedOriginRequired},
		{name: "duplicate root", cfg: servermode.Config{MediaRoots: []string{root, root}}, want: servermode.ErrInvalidMediaRoot},
		{name: "overlapping roots", cfg: servermode.Config{MediaRoots: []string{root, child}}, want: servermode.ErrInvalidMediaRoot},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := servermode.Validate(tt.cfg)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}
