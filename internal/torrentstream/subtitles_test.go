package torrentstream

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/internal/mkvsubs"
)

func TestSubtitlesBeforeTorrentCompletes(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable for fixture creation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	seedDir := t.TempDir()
	subtitle := filepath.Join(seedDir, "captions.srt")
	movie := filepath.Join(seedDir, "movie.mkv")
	if err := os.WriteFile(subtitle, []byte("1\n00:00:05,000 --> 00:00:10,000\nFirst caption\n\n2\n00:00:12,000 --> 00:00:15,000\nSecond caption\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=size=160x90:rate=10:duration=60", "-i", subtitle, "-map", "0:v", "-map", "1:s", "-c:v", "mpeg4", "-c:s", "ass", movie)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create Matroska fixture: %v: %s", err, output)
	}
	file, err := os.OpenFile(movie, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A legal trailing EBML Void proves captions need only verified pieces,
	// without waiting for an entire download. FFmpeg is used only for fixture creation.
	if _, err := file.Write([]byte{0xec, 0x12, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(file, zeroReader{}, 32<<20); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 64 << 10}
	if err := info.BuildFromFilePath(movie); err != nil {
		t.Fatal(err)
	}
	encoded, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: encoded}
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
	cfg := localConfig()
	cfg.DownloadRateLimiter = rate.NewLimiter(1<<20, 1<<20)
	session, err := openWithConfig(ctx, "", mi, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	path, err := session.Select(0)
	if err != nil {
		t.Fatal(err)
	}
	source, ok := mediasource.Lookup(path)
	if !ok {
		t.Fatal("torrent source missing")
	}
	parser := mkvsubs.New(source)
	missing, cancelMissing := context.WithTimeout(ctx, 50*time.Millisecond)
	_, err = parser.Window(missing, 0, 30)
	cancelMissing()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing pieces did not cancel: %v", err)
	}
	session.torrent.AddClientPeer(seed)
	cues, err := parser.Window(ctx, 0, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 || cues[0] != (mkvsubs.Cue{Start: 5, End: 10, Text: "First caption"}) || cues[1] != (mkvsubs.Cue{Start: 12, End: 15, Text: "Second caption"}) {
		t.Fatalf("progressive ASS captions: %+v", cues)
	}
	if completed, total := session.Progress(); completed <= 0 || completed >= total {
		t.Fatalf("captions required complete torrent: %d/%d", completed, total)
	}
}
