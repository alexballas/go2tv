package mkvsubs

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type memorySource struct {
	data      []byte
	forbidden [][2]int64
	read      atomic.Int64
	blocked   atomic.Bool
	waitAt    int64
}

func (s *memorySource) Open(ctx context.Context) (io.ReadSeekCloser, error) {
	return &memoryReader{Reader: bytes.NewReader(s.data), ctx: ctx, source: s}, nil
}
func (s *memorySource) URL() string  { return "" }
func (s *memorySource) MIME() string { return "video/x-matroska" }
func (s *memorySource) Size() int64  { return int64(len(s.data)) }

type memoryReader struct {
	*bytes.Reader
	ctx    context.Context
	source *memorySource
}

func (r *memoryReader) Close() error { return nil }
func (r *memoryReader) Read(b []byte) (int, error) {
	if r.source.blocked.Load() {
		<-r.ctx.Done()
		return 0, r.ctx.Err()
	}
	pos, _ := r.Reader.Seek(0, io.SeekCurrent)
	if r.source.waitAt > 0 && pos >= r.source.waitAt {
		<-r.ctx.Done()
		return 0, r.ctx.Err()
	}
	for _, span := range r.source.forbidden {
		if pos < span[1] && pos+int64(len(b)) > span[0] {
			return 0, errors.New("read unreceived video payload")
		}
	}
	n, err := r.Reader.Read(b)
	r.source.read.Add(int64(n))
	return n, err
}

func elem(id uint64, body ...[]byte) []byte {
	payload := bytes.Join(body, nil)
	var key [8]byte
	binary.BigEndian.PutUint64(key[:], id)
	i := 0
	for i < 7 && key[i] == 0 {
		i++
	}
	size, n := uint64(len(payload)), 1
	for size >= uint64(1)<<(7*n)-1 {
		n++
	}
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], size|uint64(1)<<(7*n))
	result := append([]byte{}, key[i:]...)
	result = append(result, encoded[8-n:]...)
	return append(result, payload...)
}
func number(id, n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	return elem(id, b[:])
}
func textTrack(n uint64, codec string) []byte {
	return elem(0xae, number(0xd7, n), number(0x83, 17), elem(0x86, []byte(codec)))
}
func subBlock(track byte, relative int16, duration uint64, text string) []byte {
	b := []byte{0x80 | track, byte(uint16(relative) >> 8), byte(relative), 0}
	b = append(b, []byte(text)...)
	return elem(0xa0, number(0x9b, duration), elem(0xa1, b))
}
func fixture(codec string, unknown bool) (*memorySource, []byte) {
	video := append([]byte{0x81, 0, 0, 0}, bytes.Repeat([]byte{0xcc}, 4<<20)...)
	caption := "{\\i1}Hello\\Nworld{\\i0}"
	if codec != "S_TEXT/UTF8" {
		caption = "0,0,Default,,0,0,0,," + caption
	} else {
		caption = "Hello\nworld"
	}
	first := elem(clusterID, number(0xe7, 0), elem(0xa3, video), subBlock(2, 5000, 10000, caption), subBlock(3, 5000, 10000, "Wrong track"))
	second := elem(clusterID, number(0xe7, 30000), elem(0xa3, video), subBlock(2, 5000, 2000, caption))
	if unknown {
		for _, cluster := range []*[]byte{&first, &second} {
			// These large clusters use a four-byte size.
			*cluster = append(append([]byte{}, (*cluster)[:4]...), append([]byte{0xff}, (*cluster)[8:]...)...)
		}
	}
	data := elem(segmentID, elem(0x1549a966, number(0x2ad7b1, 1000000)), elem(0x1654ae6b, textTrack(2, codec), textTrack(3, "S_TEXT/UTF8")), first, second)
	source := &memorySource{data: data}
	// Header bytes may be read; the multi-megabyte media body has not arrived.
	for pos := 0; ; {
		i := bytes.Index(data[pos:], bytes.Repeat([]byte{0xcc}, 16))
		if i < 0 {
			break
		}
		begin := pos + i
		source.forbidden = append(source.forbidden, [2]int64{int64(begin), int64(begin + 4<<20)})
		pos = begin + 4<<20
	}
	return source, data
}

func TestProgressiveWindowsSkipVideo(t *testing.T) {
	tt := []struct {
		name, codec string
		unknown     bool
	}{
		{"ASS", "S_TEXT/ASS", false}, {"SSA unknown clusters", "S_TEXT/SSA", true}, {"UTF8", "S_TEXT/UTF8", false},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			source, _ := fixture(tc.codec, tc.unknown)
			parser := New(source)
			for _, window := range []struct {
				start, end float64
				at         float64
			}{{0, 30, 5}, {8, 38, 5}, {33, 63, 35}, {0, 30, 5}} {
				cues, err := parser.Window(context.Background(), window.start, window.end)
				if err != nil {
					t.Fatal(err)
				}
				if len(cues) == 0 || cues[0] != (Cue{window.at, window.at + map[float64]float64{5: 10, 35: 2}[window.at], "Hello\nworld"}) {
					t.Fatalf("window %v: %+v", window, cues)
				}
				for _, cue := range cues {
					if strings.Contains(cue.Text, "Wrong") {
						t.Fatal("selected second track")
					}
				}
			}
			if source.read.Load() > 4096 {
				t.Fatalf("read video or whole media: %d bytes", source.read.Load())
			}
		})
	}
}

func TestMissingPiecesCancellationAndRetry(t *testing.T) {
	source, _ := fixture("S_TEXT/ASS", false)
	source.blocked.Store(true)
	parser := New(source)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := parser.Window(ctx, 0, 30); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel missing piece: %v", err)
	}
	source.blocked.Store(false)
	cues, err := parser.Window(context.Background(), 0, 30)
	if err != nil || len(cues) != 1 {
		t.Fatalf("retry: %+v, %v", cues, err)
	}
}

func TestAvailableCaptionsSurviveMissingFuturePieces(t *testing.T) {
	source, data := fixture("S_TEXT/ASS", false)
	// The next cluster has not arrived, but the first caption is verified.
	source.waitAt = int64(bytes.LastIndex(data, []byte{0x1f, 0x43, 0xb6, 0x75}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	w := httptest.NewRecorder()
	Handler(New(source), 0).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://host/?time=5", nil).WithContext(ctx))
	var dataWindow struct {
		Available bool
		Until     float64
		Cues      []Cue
	}
	if err := json.Unmarshal(w.Body.Bytes(), &dataWindow); err != nil {
		t.Fatalf("partial caption response %d: %s", w.Code, w.Body.String())
	}
	if w.Code != http.StatusOK || !dataWindow.Available || dataWindow.Until <= 5 || dataWindow.Until >= 35 || len(dataWindow.Cues) != 1 || dataWindow.Cues[0] != (Cue{5, 15, "Hello\nworld"}) {
		t.Fatalf("available captions delayed by missing future piece: %+v", dataWindow)
	}
}

func TestNegativeTimestampsAndLongCaptionOverlap(t *testing.T) {
	data := elem(segmentID, elem(0x1654ae6b, textTrack(2, "S_TEXT/UTF8")),
		elem(clusterID, number(0xe7, 0), subBlock(2, 0, 300000, "Long caption")),
		elem(clusterID, number(0xe7, 35000), subBlock(2, -10000, 20000, "Starts before cluster")),
		elem(clusterID, number(0xe7, 200000)))
	parser := New(&memorySource{data: data})
	cues, err := parser.Window(context.Background(), 25, 30)
	if err != nil || len(cues) != 2 || cues[1] != (Cue{25, 45, "Starts before cluster"}) {
		t.Fatalf("negative timestamp near window boundary: %+v %v", cues, err)
	}
	cues, err = parser.Window(context.Background(), 200, 230)
	if err != nil || len(cues) != 1 || cues[0] != (Cue{0, 300, "Long caption"}) {
		t.Fatalf("long caption lost after overlap window: %+v %v", cues, err)
	}
}

func TestSeekUsesMatroskaIndex(t *testing.T) {
	tracks := elem(0x1654ae6b, textTrack(2, "S_TEXT/UTF8"))
	old := elem(clusterID, number(0xe7, 0), elem(0xa3, []byte{0x81, 0, 0, 0, 0xcc}))
	near := elem(clusterID, number(0xe7, 850000))
	target := elem(clusterID, number(0xe7, 990000), subBlock(2, 0, 20000, "Visible after seek"))
	seek := func(pos uint64) []byte {
		return elem(0x114d9b74, elem(0x4dbb, number(0x53ab, cuesID), number(0x53ac, pos)))
	}
	base := len(tracks) + len(seek(0))
	cuesOffset := base + len(old) + len(near) + len(target)
	point := func(at, pos uint64) []byte { return elem(0xbb, number(0xb3, at), elem(0xb7, number(0xf1, pos))) }
	index := elem(cuesID, point(0, uint64(base)), point(850000, uint64(base+len(old))), point(990000, uint64(base+len(old)+len(near))))
	body := bytes.Join([][]byte{tracks, seek(uint64(cuesOffset)), old, near, target, index}, nil)
	data := elem(segmentID, body)
	segmentHeader := len(data) - len(body)
	source := &memorySource{data: data, forbidden: [][2]int64{{int64(segmentHeader + base + 5), int64(segmentHeader + base + len(old))}}}
	cues, err := New(source).Window(context.Background(), 1000, 1030)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 1 || cues[0] != (Cue{990, 1010, "Visible after seek"}) {
		t.Fatalf("seek cues: %+v", cues)
	}
}

func TestCaptionHTTPContract(t *testing.T) {
	source, _ := fixture("S_TEXT/ASS", false)
	handler := Handler(New(source), 5)
	// Repeat a valid window to catch mutation of cached source timestamps when
	// the HTTP handler adjusts returned cues for a transcoded stream.
	tt := []struct {
		method, query string
		status        int
	}{{"GET", "time=2", 200}, {"GET", "time=2", 200}, {"GET", "time=NaN", 400}, {"GET", "time=-1", 400}, {"GET", "", 400}, {"OPTIONS", "", 204}, {"POST", "time=0", 405}}
	for _, tc := range tt {
		t.Run(tc.method+tc.query, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://host/subtitles?"+tc.query, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("Access-Control-Allow-Origin") != "*" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response: %d %v", w.Code, w.Header())
			}
			if tc.status == 200 {
				var data struct {
					Available bool
					Until     float64
					Cues      []Cue
				}
				if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
					t.Fatal(err)
				}
				if !data.Available || data.Until != 32 || len(data.Cues) != 2 || data.Cues[0] != (Cue{0, 10, "Hello\nworld"}) || data.Cues[1] != (Cue{30, 32, "Hello\nworld"}) {
					t.Fatalf("transcoded timeline: %+v", data)
				}
			}
		})
	}
	unsupported := &memorySource{data: elem(segmentID, elem(0x1654ae6b, textTrack(2, "S_HDMV/PGS")), elem(clusterID, number(0xe7, 0)))}
	w := httptest.NewRecorder()
	Handler(New(unsupported), 0).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://host/?time=0", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":false`) {
		t.Fatalf("unsupported subtitle track: %s", w.Body.String())
	}
}
