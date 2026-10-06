package torrentstream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// Inspect decoded pixels, rather than FFmpeg arguments: captions must actually
// appear on the video, including after seeks and with nonzero media timestamps.
func TestBurnEmbeddedTorrentSubtitleTiming(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	filters, err := exec.Command(ffmpeg, "-hide_banner", "-filters").Output()
	if err != nil || !bytes.Contains(filters, []byte(" subtitles ")) {
		t.Skip("ffmpeg subtitles filter unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, origin := range []int{0, 10} {
		t.Run("origin "+strconv.Itoa(origin), func(t *testing.T) {
			seedDir := t.TempDir()
			captions := filepath.Join(seedDir, "captions.srt")
			movie := filepath.Join(seedDir, "movie.mkv")
			if err := os.WriteFile(captions, []byte("1\n00:00:02,000 --> 00:00:04,000\nFirst caption\n\n2\n00:00:28,000 --> 00:00:31,000\nSecond caption\n"), 0600); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(seedDir, "external.vtt")
			if err := os.WriteFile(external, []byte("WEBVTT\n\n00:00:03.000 --> 00:00:05.000\nExternal caption\n"), 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "color=c=black:size=160x90:rate=2:duration=32", "-i", captions,
				"-map", "0:v", "-map", "1:s", "-c:v", "mpeg4", "-g", "2", "-c:s", "ass", "-output_ts_offset", strconv.Itoa(origin), movie)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("create fixture: %v: %s", err, output)
			}
			data, err := os.ReadFile(movie)
			if err != nil {
				t.Fatal(err)
			}
			captionOffset := bytes.Index(data, []byte("First caption"))
			if captionOffset < 0 {
				t.Fatal("fixture caption missing")
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
				t.Fatal("torrent source unavailable")
			}
			blocked, cancelBlocked := context.WithTimeout(ctx, 50*time.Millisecond)
			var output bytes.Buffer
			var process exec.Cmd
			err = utils.ServeDLNATranscodedStream(blocked, &output, path, &process, &utils.TranscodeOptions{FFmpegPath: ffmpeg, TorrentSource: source})
			cancelBlocked()
			if !errors.Is(err, context.DeadlineExceeded) || output.Len() != 0 {
				t.Fatalf("missing pieces must cancel burn preparation: %v, bytes=%d", err, output.Len())
			}
			session.torrent.AddClientPeer(seed)
			for _, tc := range []struct {
				name               string
				chromecast         bool
				seek               int
				external, disabled bool
				broken             bool
			}{
				{name: "DLNA"}, {name: "DLNA seek", seek: 7},
				{name: "Chromecast", chromecast: true}, {name: "Chromecast seek", chromecast: true, seek: 7},
				{name: "DLNA external priority", external: true}, {name: "Chromecast external priority", chromecast: true, external: true},
				{name: "DLNA captions disabled", disabled: true}, {name: "Chromecast captions disabled", chromecast: true, disabled: true},
				{name: "DLNA caption read failure", broken: true}, {name: "Chromecast caption read failure", chromecast: true, broken: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					output.Reset()
					var process exec.Cmd
					opts := &utils.TranscodeOptions{FFmpegPath: ffmpeg, TorrentSource: source, SeekSeconds: tc.seek, SubtitleSize: utils.SubtitleSizeMedium}
					if tc.external {
						opts.SubsPath = external
					}
					if tc.disabled {
						opts.TorrentSource = nil
					}
					if tc.broken {
						opts.TorrentSource = &brokenCaptionSource{Source: source, offset: int64(captionOffset)}
					}
					if tc.chromecast {
						err = utils.ServeChromecastTranscodedStream(ctx, &output, path, &process, opts)
					} else {
						err = utils.ServeDLNATranscodedStream(ctx, &output, path, &process, opts)
					}
					if err != nil {
						t.Fatal(err)
					}
					decode := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-i", "pipe:0", "-map", "0:v", "-pix_fmt", "gray", "-fps_mode", "passthrough", "-f", "rawvideo", "pipe:1")
					decode.Stdin = bytes.NewReader(output.Bytes())
					frames, err := decode.Output()
					if err != nil {
						t.Fatalf("decode output: %v", err)
					}
					const pixels = 160 * 90
					wantFrames := (32 - tc.seek) * 2
					if len(frames) != wantFrames*pixels {
						t.Fatalf("decoded frames=%d want %d", len(frames)/pixels, wantFrames)
					}
					for i := range wantFrames {
						at := float64(tc.seek) + float64(i)/2
						wantCaption := (at >= 2 && at < 4) || (at >= 28 && at < 31)
						switch {
						case tc.external:
							wantCaption = at >= 3 && at < 5
						case tc.disabled || tc.broken:
							wantCaption = false
						}
						hasCaption := false
						for _, pixel := range frames[i*pixels : (i+1)*pixels] {
							if pixel > 100 {
								hasCaption = true
								break
							}
						}
						if hasCaption != wantCaption {
							t.Fatalf("caption at source %.1fs: visible=%v want %v", at, hasCaption, wantCaption)
						}
					}
				})
			}
		})
	}
}

// Metadata remains readable; only subtitle payload reads fail. The video's
// independent reader must keep producing all frames without captions.
type brokenCaptionSource struct {
	mediasource.Source
	offset int64
}

func (s *brokenCaptionSource) Open(ctx context.Context) (io.ReadSeekCloser, error) {
	r, err := s.Source.Open(ctx)
	if err != nil {
		return nil, err
	}
	return &brokenCaptionReader{ReadSeekCloser: r, offset: s.offset}, nil
}

type brokenCaptionReader struct {
	io.ReadSeekCloser
	offset int64
}

func (r *brokenCaptionReader) Read(data []byte) (int, error) {
	pos, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	if pos <= r.offset && pos+int64(len(data)) > r.offset {
		return 0, io.ErrUnexpectedEOF
	}
	return r.ReadSeekCloser.Read(data)
}

// Interleaved media fills every piece. Inspect actual output at startup, rather
// than relying on an undownloaded Void after all of the playable media.
func TestTorrentSubtitleBurnStartsBeforeMediaDownloadCompletes(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	filters, err := exec.Command(ffmpeg, "-hide_banner", "-filters").Output()
	if err != nil || !bytes.Contains(filters, []byte(" subtitles ")) {
		t.Skip("ffmpeg subtitles filter unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	seedDir := t.TempDir()
	captions := filepath.Join(seedDir, "captions.srt")
	movie := filepath.Join(seedDir, "movie.mkv")
	if err := os.WriteFile(captions, []byte("1\n00:00:02,000 --> 00:00:04,000\nFirst caption\n\n2\n00:01:30,000 --> 00:01:34,000\nLate caption\n"), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=120",
		"-f", "lavfi", "-i", "sine=frequency=1000:duration=120", "-i", captions,
		"-map", "0:v", "-map", "1:a", "-map", "2:s", "-c:v", "libx264",
		"-preset", "ultrafast", "-b:v", "2M", "-minrate", "2M", "-maxrate", "2M", "-bufsize", "2M",
		"-x264-params", "nal-hrd=cbr", "-g", "25", "-c:a", "aac", "-c:s", "srt", movie)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create interleaved fixture: %v: %s", err, output)
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
	for _, chromecast := range []bool{false, true} {
		name := "DLNA"
		if chromecast {
			name = "Chromecast"
		}
		t.Run(name, func(t *testing.T) {
			cfg := localConfig()
			cfg.DownloadRateLimiter = rate.NewLimiter(2<<20, 64<<10)
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
				t.Fatal("torrent source unavailable")
			}
			session.torrent.AddClientPeer(seed)
			streamCtx, stop := context.WithCancel(ctx)
			defer stop()
			output := &startupWriter{session: session, stop: stop}
			var process exec.Cmd
			opts := &utils.TranscodeOptions{FFmpegPath: ffmpeg, TorrentSource: source, SubtitleSize: utils.SubtitleSizeMedium}
			if chromecast {
				err = utils.ServeChromecastTranscodedStream(streamCtx, output, path, &process, opts)
			} else {
				err = utils.ServeDLNATranscodedStream(streamCtx, output, path, &process, opts)
			}
			if !errors.Is(err, context.Canceled) || !output.started {
				t.Fatalf("startup did not produce media: %v, bytes=%d", err, output.Len())
			}
			if output.completed >= output.total {
				t.Fatalf("startup required all playable media: %d/%d", output.completed, output.total)
			}
			t.Logf("startup downloaded %d/%d bytes", output.completed, output.total)
			decode := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-i", "pipe:0", "-map", "0:v", "-frames:v", "1", "-pix_fmt", "gray", "-f", "rawvideo", "pipe:1")
			decode.Stdin = bytes.NewReader(output.Bytes())
			frames, err := decode.Output()
			if err != nil || len(frames) != 640*360 {
				t.Fatalf("startup output must contain a decodable frame: %v, bytes=%d", err, len(frames))
			}
		})
	}
}

type startupWriter struct {
	bytes.Buffer
	session          *Session
	stop             context.CancelFunc
	started          bool
	completed, total int64
}

func (w *startupWriter) Write(data []byte) (int, error) {
	n, err := w.Buffer.Write(data)
	// Both containers must have output beyond just their initial header.
	if !w.started && w.Len() >= 64<<10 {
		w.started = true
		w.completed, w.total = w.session.Progress()
		w.stop()
	}
	return n, err
}
