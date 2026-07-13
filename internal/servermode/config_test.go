package servermode

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateCLIFlagMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		server    bool
		listenSet bool
		roots     []string
		origins   []string
		legacy    []string
		args      []string
		wantErr   error
	}{
		{name: "legacy mode", wantErr: nil},
		{name: "server minimal", server: true, roots: []string{"root"}},
		{name: "listen without server", listenSet: true, wantErr: ErrServerFlagWithoutMode},
		{name: "root without server", roots: []string{"root"}, wantErr: ErrServerFlagWithoutMode},
		{name: "origin without server", origins: []string{"http://host:1"}, wantErr: ErrServerFlagWithoutMode},
		{name: "server missing root", server: true, wantErr: ErrInvalidMediaRoot},
		{name: "server version", server: true, roots: []string{"root"}, legacy: []string{"-version"}, wantErr: ErrServerFlagConflict},
		{name: "server video", server: true, roots: []string{"root"}, legacy: []string{"-v"}, wantErr: ErrServerFlagConflict},
		{name: "server URL", server: true, roots: []string{"root"}, legacy: []string{"-u"}, wantErr: ErrServerFlagConflict},
		{name: "server subtitles", server: true, roots: []string{"root"}, legacy: []string{"-s"}, wantErr: ErrServerFlagConflict},
		{name: "server target", server: true, roots: []string{"root"}, legacy: []string{"-t"}, wantErr: ErrServerFlagConflict},
		{name: "server transcode", server: true, roots: []string{"root"}, legacy: []string{"-tc"}, wantErr: ErrServerFlagConflict},
		{name: "server list", server: true, roots: []string{"root"}, legacy: []string{"-l"}, wantErr: ErrServerFlagConflict},
		{name: "positional legacy", args: []string{"file"}, wantErr: ErrPositionalArguments},
		{name: "positional server", server: true, roots: []string{"root"}, args: []string{"file"}, wantErr: ErrPositionalArguments},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCLI(tt.server, tt.listenSet, tt.roots, tt.origins, tt.legacy, tt.args)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ValidateCLI() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		cfg     Config
		wantErr error
	}{
		{name: "loopback", cfg: Config{Listen: DefaultListen, MediaRoots: []string{root}}},
		{name: "wildcard allowed", cfg: Config{Listen: "0.0.0.0:9666", MediaRoots: []string{root}, AllowedOrigins: []string{"http://tv.local:9666"}}},
		{name: "wildcard no origin", cfg: Config{Listen: "0.0.0.0:9666", MediaRoots: []string{root}}, wantErr: ErrAllowedOriginRequired},
		{name: "hostname listen", cfg: Config{Listen: "example.test:9666", MediaRoots: []string{root}}, wantErr: ErrInvalidListen},
		{name: "missing port", cfg: Config{Listen: "127.0.0.1", MediaRoots: []string{root}}, wantErr: ErrInvalidListen},
		{name: "origin missing port", cfg: Config{Listen: "0.0.0.0:9666", MediaRoots: []string{root}, AllowedOrigins: []string{"http://tv.local"}}, wantErr: ErrInvalidOrigin},
		{name: "TLS unavailable", cfg: Config{Listen: "0.0.0.0:9666", MediaRoots: []string{root}, AllowedOrigins: []string{"https://tv.local:9666"}}, wantErr: ErrInvalidOrigin},
		{name: "null origin", cfg: Config{Listen: "0.0.0.0:9666", MediaRoots: []string{root}, AllowedOrigins: []string{"null"}}, wantErr: ErrInvalidOrigin},
		{name: "duplicate origin", cfg: Config{Listen: "0.0.0.0:9666", MediaRoots: []string{root}, AllowedOrigins: []string{"http://TV.local:9666", "http://tv.local:9666"}}, wantErr: ErrInvalidOrigin},
		{name: "duplicate root", cfg: Config{Listen: DefaultListen, MediaRoots: []string{root, root}}, wantErr: ErrInvalidMediaRoot},
		{name: "overlapping root", cfg: Config{Listen: DefaultListen, MediaRoots: []string{root, child}}, wantErr: ErrInvalidMediaRoot},
		{name: "missing root", cfg: Config{Listen: DefaultListen}, wantErr: ErrInvalidMediaRoot},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Validate(tt.cfg)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
