package torrentstream

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/utils"
)

// Real FFmpeg must seek through HTTP to unreceived torrent data. Counting
// decoded frames catches silently ignored -ss (the former stdin behavior).
func TestFFmpegSeeksBeforeTorrentCompletes(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	seedDir := t.TempDir()
	movie := filepath.Join(seedDir, "movie.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10", "-t", "12", "-c:v", "mpeg4", "-g", "10", "-q:v", "5", movie)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create fixture: %v: %s", err, output)
	}
	// A legal MP4 free atom keeps the download larger than the sample data.
	// This lets us prove transcoding never requires the complete file.
	file, err := os.OpenFile(movie, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	const padding = 32 << 20
	header := make([]byte, 8)
	binary.BigEndian.PutUint32(header, padding)
	copy(header[4:], "free")
	if _, err := file.Write(header); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(file, zeroReader{}, padding-8); err != nil {
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
	tt := []struct {
		name, extension string
		chromecast      bool
		readerInput     bool
	}{
		{name: "DLNA", extension: ".ts"},
		{name: "Chromecast", extension: ".mp4", chromecast: true},
		{name: "WebUI DLNA reader", extension: ".ts", readerInput: true},
		{name: "WebUI Chromecast reader", extension: ".mp4", chromecast: true, readerInput: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
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
			var input any = path
			if tc.readerInput {
				source, ok := mediasource.Lookup(path)
				if !ok {
					t.Fatal("torrent source missing")
				}
				reader, err := source.Open(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				input = reader
			}
			var output bytes.Buffer
			result := make(chan error, 1)
			go func() {
				var command exec.Cmd
				if tc.chromecast {
					result <- utils.ServeChromecastTranscodedStream(ctx, &output, input, &command, &utils.TranscodeOptions{FFmpegPath: ffmpeg, SeekSeconds: 7})
				} else {
					result <- utils.ServeTranscodedStream(ctx, &output, input, &command, ffmpeg, "", 7, utils.SubtitleSizeMedium)
				}
			}()
			select {
			case err := <-result:
				t.Fatalf("transcode ended without pieces: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
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
				t.Fatalf("transcode waited for whole file: %d/%d", completed, total)
			}
			if tc.readerInput {
				duration, err := utils.DurationForMediaReaderSeconds(ctx, ffmpeg, input.(io.ReadSeekCloser))
				if err != nil || duration != 12 {
					t.Fatalf("torrent reader duration = %v, err=%v", duration, err)
				}
			}
			transcoded := filepath.Join(t.TempDir(), "transcoded"+tc.extension)
			if err := os.WriteFile(transcoded, output.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			probe := exec.CommandContext(ctx, ffprobe, "-v", "error", "-count_frames", "-select_streams", "v:0", "-show_entries", "stream=nb_read_frames", "-of", "csv=p=0", transcoded)
			frames, err := probe.Output()
			if err != nil {
				t.Fatalf("probe transcoded media: %v", err)
			}
			// MPEG-TS can report the stream both under program and at root.
			count, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(string(frames), "\n", 2)[0]))
			if err != nil || count != 50 {
				t.Fatalf("seek decoded %q frames, expected 50 (12s - 7s at 10fps)", frames)
			}
		})
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
