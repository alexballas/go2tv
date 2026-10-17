package mkvsubs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSelectedTextBurnReadsOnlyStartupWindow(t *testing.T) {
	video := bytes.Repeat([]byte{0xcc}, 4<<20)
	tail := bytes.Repeat([]byte{0xdd}, 4<<20)
	tracks := elem(0x1654ae6b, textTrack(2, "S_HDMV/PGS"), textTrack(3, "S_TEXT/UTF8"), textTrack(7, "S_TEXT/UTF8"))
	data := elem(segmentID, tracks,
		elem(clusterID, number(0xe7, 0), elem(0xa3, append([]byte{0x81, 0, 0, 0}, video...)),
			subBlock(3, 1000, 2000, "Other language"), subBlock(7, 1000, 2000, "Selected language")),
		elem(clusterID, number(0xe7, 50000)),
		elem(clusterID, number(0xe7, 3600000), elem(0xa3, append([]byte{0x81, 0, 0, 0}, tail...))))
	tt := []struct {
		name  string
		index int
		want  string
	}{
		{name: "bitmap ordinal retained", index: 0},
		{name: "first text track", index: 1, want: "Other language"},
		{name: "selected second text track", index: 2, want: "Selected language"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			source := &memorySource{data: data}
			for _, body := range [][]byte{video, tail} {
				pos := bytes.Index(data, body)
				source.forbidden = append(source.forbidden, [2]int64{int64(pos), int64(pos + len(body))})
			}
			parser := NewForSubtitle(source, tc.index)
			_, err := parser.PrepareBurn(t.Context())
			if tc.want == "" {
				if !errors.Is(err, ErrNoSubtitles) {
					t.Fatalf("bitmap selection error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cues, err := parser.Window(t.Context(), 0, 10)
			if err != nil || len(cues) != 1 || cues[0].Text != tc.want || cues[0].Start != 1 || cues[0].End != 3 {
				t.Fatalf("startup captions = %+v, error = %v", cues, err)
			}
			if source.read.Load() > 4096 {
				t.Fatalf("startup scanned media beyond its caption window: %d bytes", source.read.Load())
			}
		})
	}
}

func TestIndexedBurnPreservesLongCueAtFreshSeek(t *testing.T) {
	tt := []struct {
		name     string
		duration bool
		track    uint64
		partial  bool
		noIndex  bool
	}{
		{name: "indexed long caption", duration: true, track: 2},
		{name: "missing cue duration", track: 2},
		{name: "video-only index", duration: true, track: 1},
		{name: "sparse subtitle index with durations", duration: true, track: 2, partial: true},
		{name: "no cue index", noIndex: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			video := bytes.Repeat([]byte{0xcc}, 4<<20)
			tracks := elem(0x1654ae6b, textTrack(2, "S_TEXT/UTF8"))
			first := elem(clusterID, number(0xe7, 0), subBlock(2, 0, 195000, "Long caption"))
			middle := elem(clusterID, number(0xe7, 60000), elem(0xa3, append([]byte{0x81, 0, 0, 0}, video...)))
			last := elem(clusterID, number(0xe7, 180000))
			if tc.partial {
				last = elem(clusterID, number(0xe7, 179000), subBlock(2, 0, 2000, "Short caption"))
			}
			seek := func(pos uint64) []byte {
				return elem(0x114d9b74, elem(0x4dbb, number(0x53ab, cuesID), number(0x53ac, pos)))
			}
			base := len(tracks) + len(seek(0))
			point := func(at, pos, track, duration uint64) []byte {
				position := append(number(0xf7, track), number(0xf1, pos)...)
				if duration != 0 {
					position = append(position, number(0xb2, duration)...)
				}
				return elem(0xbb, number(0xb3, at), elem(0xb7, position))
			}
			duration := uint64(0)
			if tc.duration {
				duration = 195000
			}
			index := elem(cuesID, point(0, uint64(base), tc.track, duration),
				point(60000, uint64(base+len(first)), 1, 0), point(180000, uint64(base+len(first)+len(middle)), 1, 0))
			if tc.partial {
				index = elem(cuesID, point(60000, uint64(base+len(first)), 1, 0), point(179000, uint64(base+len(first)+len(middle)), 2, 2000))
			}
			data := elem(segmentID, tracks, seek(uint64(base+len(first)+len(middle)+len(last))), first, middle, last, index)
			if tc.noIndex {
				data = elem(segmentID, tracks, first, middle, last)
			}
			pos := bytes.Index(data, video)
			source := &memorySource{data: data, forbidden: [][2]int64{{int64(pos), int64(pos + len(video))}}}
			parser := NewForSubtitle(source, 0)
			_, err := parser.PrepareBurnForSeek(t.Context(), 180)
			if err != nil {
				t.Fatal(err)
			}
			cues, err := parser.Window(t.Context(), 180, 190)
			want := 1
			if tc.partial {
				want = 2
			}
			if err != nil || len(cues) != want || cues[0] != (Cue{0, 195, "Long caption"}) {
				t.Fatalf("overlapping cue at seek = %+v, error = %v", cues, err)
			}
			cues, err = parser.Window(t.Context(), 190, 195)
			if err != nil || len(cues) != 1 || cues[0] != (Cue{0, 195, "Long caption"}) {
				t.Fatalf("overlapping cue in next window = %+v, error = %v", cues, err)
			}
			cues, err = parser.Window(t.Context(), 196, 206)
			if err != nil || len(cues) != 0 {
				t.Fatalf("expired caption at seek = %+v, error = %v", cues, err)
			}
			if source.read.Load() > 4096 {
				t.Fatalf("seek read media payload: %d bytes", source.read.Load())
			}
		})
	}
}

func TestLocalSeekPreservesFutureCueWithPositiveBlockTimestamp(t *testing.T) {
	data := elem(segmentID,
		elem(0x1549a966, number(0x2ad7b1, 10000000)),
		elem(0x1654ae6b, textTrack(2, "S_TEXT/UTF8")),
		elem(clusterID, number(0xe7, 0), subBlock(2, 0, 30000, "Long caption"), subBlock(2, 19000, 500, "Later caption in earlier cluster")),
		elem(clusterID, number(0xe7, 6000)), elem(clusterID, number(0xe7, 40000)))
	parser := NewForSubtitle(&memorySource{data: data}, 0)
	if _, err := parser.PrepareBurnForSeek(t.Context(), 180); err != nil {
		t.Fatal(err)
	}
	cues, err := parser.Window(t.Context(), 180, 190)
	if err != nil || len(cues) != 1 || cues[0].Text != "Long caption" {
		t.Fatalf("first seek window = %+v, error = %v", cues, err)
	}
	cues, err = parser.Window(t.Context(), 190, 200)
	if err != nil || len(cues) != 2 || cues[1] != (Cue{190, 195, "Later caption in earlier cluster"}) {
		t.Fatalf("future caption disappeared across windows: %+v, error = %v", cues, err)
	}
}

func TestStyledBurnReadsIndexedFontsWithoutMediaPayload(t *testing.T) {
	const header = "[Script Info]\nScriptType: v4.00+\nPlayResX: 320\nPlayResY: 180\n[V4+ Styles]\n"
	const packet = "12,7,Sign,Actor,0012,0034,0056,,{\\fnGo Mono\\pos(15,20)\\c&H00FF00&}Hello\\Nworld"
	font := []byte("attached font bytes")
	media := bytes.Repeat([]byte{0xcc}, 4<<20)
	picture := bytes.Repeat([]byte{0xdd}, 2<<20)
	tracks := elem(0x1654ae6b,
		elem(0xae, number(0x83, 1), number(0xd7, 1), elem(0xe0, number(0xb0, 320), number(0xba, 180))),
		elem(0xae, number(0x83, 17), number(0xd7, 2), elem(0x86, []byte("S_TEXT/ASS")), elem(0x63a2, []byte(header))))
	cluster := elem(clusterID, number(0xe7, 0), elem(0xa3, append([]byte{0x81, 0, 0, 0}, media...)), subBlock(2, 1000, 12000, packet))
	attachments := elem(attachmentsID,
		elem(0x61a7, elem(0x466e, []byte("../../outside.TTF")), elem(0x465c, font)),
		elem(0x61a7, elem(0x466e, []byte("cover.jpg")), elem(0x465c, picture)))
	seek := func(pos uint64) []byte {
		return elem(0x114d9b74, elem(0x4dbb, number(0x53ab, attachmentsID), number(0x53ac, pos)))
	}
	index := seek(uint64(len(seek(0)) + len(tracks) + len(cluster)))
	data := elem(segmentID, index, tracks, cluster, attachments)
	source := &memorySource{data: data}
	for _, payload := range [][]byte{media, picture} {
		pos := bytes.Index(data, payload)
		source.forbidden = append(source.forbidden, [2]int64{int64(pos), int64(pos + len(payload))})
	}
	parser := New(source)
	origin, err := parser.PrepareBurn(context.Background())
	if err != nil || origin != 0 {
		t.Fatalf("prepare styled burn: origin=%v error=%v", origin, err)
	}
	metadata := parser.BurnMetadata()
	if !metadata.Styled() || metadata.Header != header || metadata.Width != 320 || metadata.Height != 180 {
		t.Fatalf("missing script or canvas: %+v", metadata)
	}
	cues, err := parser.Window(context.Background(), 10, 20)
	if err != nil || len(cues) != 1 || cues[0].Text != packet || cues[0].Start != 1 || cues[0].End != 13 {
		t.Fatalf("lost original styling/timeline: %+v, %v", cues, err)
	}
	dir := t.TempDir()
	if err := parser.ExtractFonts(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("font extraction: %v, %v", files, err)
	}
	extracted, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil || !bytes.Equal(extracted, font) {
		t.Fatalf("attached font lost: %q, %v", extracted, err)
	}
	if source.read.Load() > 4096 {
		t.Fatalf("font preparation read media/non-font bodies: %d bytes", source.read.Load())
	}
}
