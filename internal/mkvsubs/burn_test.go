package mkvsubs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

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
