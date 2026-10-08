package torrentstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"go2tv.app/go2tv/v2/httphandlers"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/soapcalls"
)

// Discovery is disabled: tests exchange data only between explicit local peers.
func localConfig() *torrent.ClientConfig {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = ""
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.ListenPort = 0
	cfg.NoDHT = true
	cfg.NoDefaultPortForwarding = true
	cfg.DisableTrackers = true
	cfg.DisablePEX = true
	cfg.DisableUTP = true
	cfg.DisableIPv6 = true
	cfg.DisableWebtorrent = true
	return cfg
}

func torrentFixture(t *testing.T) (*metainfo.MetaInfo, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	data := make([]byte, 32<<20)
	for i := range data {
		data[i] = byte(i*31 + i/(64<<10))
	}
	path := filepath.Join(dir, "movie.mp4")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 64 << 10}
	if err := info.BuildFromFilePath(path); err != nil {
		t.Fatal(err)
	}
	encoded, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	return &metainfo.MetaInfo{InfoBytes: encoded}, dir, data
}

func TestProgressiveRangeSeek(t *testing.T) {
	mi, seedDir, data := torrentFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg := localConfig()
	cfg.DownloadRateLimiter = rate.NewLimiter(1<<20, 1<<20)
	session, err := openWithConfig(ctx, "", mi, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	files, err := session.Files(ctx)
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v, err = %v", files, err)
	}
	path, err := session.Select(files[0].Index)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := mediasource.Lookup(path)
	if !ok {
		t.Fatal("selected source unavailable")
	}
	server := httphandlers.NewServer("")
	server.AddHandler("/movie.mp4", &soapcalls.TVPayload{MediaType: "video/mp4", Seekable: true}, nil, path)

	// A renderer HEAD must work even when no pieces or peers are available.
	headRequest, _ := http.NewRequestWithContext(ctx, http.MethodHead, source.URL(), nil)
	head, err := http.DefaultClient.Do(headRequest)
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != http.StatusOK || head.ContentLength != int64(len(data)) {
		t.Fatalf("HEAD = %d, length = %d", head.StatusCode, head.ContentLength)
	}
	if completed, _ := session.Progress(); completed != 0 {
		t.Fatal("HEAD downloaded content")
	}

	const offset = 24 << 20
	const length = 4096
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, source.URL(), nil)
	request.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
	result := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			result <- err
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err == nil && (response.StatusCode != http.StatusPartialContent || !bytes.Equal(body, data[offset:offset+length])) {
			err = fmt.Errorf("range returned %d, %d bytes (expected verified requested region)", response.StatusCode, len(body))
		}
		result <- err
	}()
	select {
	case err := <-result:
		t.Fatalf("missing piece did not wait: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	seedCfg := localConfig()
	seedCfg.DataDir, seedCfg.Seed = seedDir, true
	seedStorage := storage.NewFileOpts(storage.NewFileClientOpts{ClientBaseDir: seedDir})
	defer seedStorage.Close()
	seedCfg.DefaultStorage = seedStorage
	seed, err := torrent.NewClient(seedCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer seed.Close()
	seedTorrent, err := seed.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}
	seedTorrent.DownloadAll()
	session.torrent.AddClientPeer(seed)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if completed, total := session.Progress(); completed >= total {
		t.Fatalf("range seek waited for full download: %d/%d", completed, total)
	}

	// The cast HTTP handler must resolve the logical path too; sparse disk
	// contents must never leak through the existing DLNA/Chromecast server.
	t.Run("cast server", func(t *testing.T) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://cast/movie.mp4", nil)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
		req.Header.Set("getcontentFeatures.dlna.org", "1")
		w := httptest.NewRecorder()
		server.ServeMediaHandler()(w, req)
		if w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), data[offset:offset+length]) {
			t.Fatalf("cast range = %d, %d bytes", w.Code, w.Body.Len())
		}
		if features := w.Header()["contentFeatures.dlna.org"]; len(features) != 1 || features[0] != "DLNA.ORG_OP=01;DLNA.ORG_CI=0" {
			t.Fatalf("seek features = %q", features)
		}
	})
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := mediasource.Lookup(path); ok {
		t.Fatal("closed source remains registered")
	}
	if _, err := os.Stat(session.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache remains: %v", err)
	}
}

func TestDeselectStopsDownloadAndReleasesSource(t *testing.T) {
	mi, _, _ := torrentFixture(t)
	session, err := openWithConfig(context.Background(), "", mi, localConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	path, err := session.Select(0)
	if err != nil {
		t.Fatal(err)
	}
	session.Deselect()
	if priority := session.torrent.Files()[0].Priority(); priority != torrent.PiecePriorityNone {
		t.Fatalf("deselected file still scheduled for download: %v", priority)
	}
	if _, ok := mediasource.Lookup(path); ok {
		t.Fatal("deselected source remains registered")
	}
	path, err = session.Select(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mediasource.Lookup(path); !ok {
		t.Fatal("retry source unavailable")
	}
	if priority := session.torrent.Files()[0].Priority(); priority == torrent.PiecePriorityNone {
		t.Fatal("retry did not resume downloading")
	}
}

func TestMissingPieceCancellation(t *testing.T) {
	mi, _, _ := torrentFixture(t)
	session, err := openWithConfig(context.Background(), "", mi, localConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	path, err := session.Select(0)
	if err != nil {
		t.Fatal(err)
	}
	source, _ := mediasource.Lookup(path)
	ctx, cancel := context.WithCancel(context.Background())
	reader, err := source.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Seek(24<<20, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := reader.Read(make([]byte, 1024)); result <- err }()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("read cancellation = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled read still waits for peers")
	}
}
