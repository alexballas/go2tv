package mkvsubs

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func captionOriginFixture(origin uint64, partial bool) *memorySource {
	track := elem(0xae, number(0xd7, 2), number(0x83, 17), elem(0x86, []byte("S_TEXT/ASS")),
		elem(0x63a2, []byte("[Script Info]\nScriptType: v4.00+\n")))
	video := bytes.Repeat([]byte{0xcc}, 4<<20)
	videoTrack := elem(0xae, number(0xd7, 1), number(0x83, 1))
	data := elem(segmentID, elem(0x1654ae6b, videoTrack, track),
		elem(clusterID, number(0xe7, origin), elem(0xa3, append([]byte{0x81, 0, 0, 0}, video...)),
			subBlock(2, 1000, 2000, "0,0,Default,,0,0,0,,{\\i1}Caption{\\i0}")),
		elem(clusterID, number(0xe7, origin+35000)))
	begin := bytes.Index(data, video)
	source := &memorySource{data: data, forbidden: [][2]int64{{int64(begin), int64(begin + len(video))}}}
	if partial {
		source.waitAt = int64(bytes.LastIndex(data, []byte{0x1f, 0x43, 0xb6, 0x75}))
	}
	return source
}

func TestCaptionHTTPAudioOriginSkipsMediaPayload(t *testing.T) {
	tt := []struct {
		name       string
		relative   int16
		delay      uint64
		blockGroup bool
		wantStart  float64
	}{
		{name: "audio precedes first video", relative: -1000, wantStart: 2},
		{name: "block group audio precedes video", relative: -1000, blockGroup: true, wantStart: 2},
		{name: "AAC codec delay", delay: 23219955, wantStart: 1.023},
		{name: "Opus codec delay rounds to timestamp units", delay: 6500000, wantStart: 1.007},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			video := bytes.Repeat([]byte{0xcc}, 4<<20)
			tracks := elem(0x1654ae6b,
				elem(0xae, number(0xd7, 1), number(0x83, 1)),
				elem(0xae, number(0xd7, 2), number(0x83, 2), number(0x56aa, tc.delay)),
				textTrack(3, "S_TEXT/UTF8"))
			audio := elem(0xa3, []byte{0x82, byte(uint16(tc.relative) >> 8), byte(tc.relative), 0})
			if tc.blockGroup {
				audio = elem(0xa0, elem(0xa1, []byte{0x82, byte(uint16(tc.relative) >> 8), byte(tc.relative), 0}))
			}
			data := elem(segmentID, tracks, elem(clusterID, number(0xe7, 5000),
				elem(0xa3, append([]byte{0x81, 0, 0, 0}, video...)), audio, subBlock(3, 1000, 2000, "Caption")))
			begin := bytes.Index(data, video)
			source := &memorySource{data: data, forbidden: [][2]int64{{int64(begin), int64(begin + len(video))}}}
			handler := Handler(New(source), 0, true)
			for range 2 {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://host/?time=0", nil))
				var result struct{ Cues []Cue }
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusOK || len(result.Cues) != 1 || result.Cues[0].Text != "Caption" ||
					math.Abs(result.Cues[0].Start-tc.wantStart) > 1e-9 || math.Abs(result.Cues[0].End-(tc.wantStart+2)) > 1e-9 {
					t.Fatalf("audio-origin captions = %+v, status=%d, want start=%v", result.Cues, response.Code, tc.wantStart)
				}
			}
			if source.read.Load() > 4096 {
				t.Fatalf("origin lookup read media payload: %d bytes", source.read.Load())
			}
		})
	}
}

func TestCaptionHTTPMediaOrigin(t *testing.T) {
	tt := []struct {
		name       string
		origin     uint64
		seek       float64
		transcoded bool
		partial    bool
		want       Cue
	}{
		{name: "zero origin", transcoded: true, want: Cue{1, 3, "Caption"}},
		{name: "nonzero origin", origin: 5250, transcoded: true, want: Cue{1, 3, "Caption"}},
		{name: "origin beyond receiver window", origin: 70000, transcoded: true, want: Cue{1, 3, "Caption"}},
		{name: "seek during active caption", origin: 5250, seek: 2, transcoded: true, want: Cue{-1, 1, "Caption"}},
		{name: "partial window includes origin and seek", origin: 5250, seek: 2, transcoded: true, partial: true, want: Cue{-1, 1, "Caption"}},
		{name: "native playback retains container timeline", origin: 5250, want: Cue{6.25, 8.25, "Caption"}},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			source := captionOriginFixture(tc.origin, tc.partial)
			handler := Handler(New(source), tc.seek, tc.transcoded)
			// A repeated request must not compound offsets in cached captions.
			for range 2 {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://host/?time=0", nil).WithContext(ctx))
				cancel()
				var result struct {
					Available bool
					Until     float64
					Cues      []Cue
				}
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatalf("caption response %d: %s: %v", response.Code, response.Body.String(), err)
				}
				if response.Code != http.StatusOK || !result.Available || len(result.Cues) != 1 || result.Cues[0] != tc.want {
					t.Fatalf("captions = %+v, status=%d, want %+v", result, response.Code, tc.want)
				}
				until := float64(30)
				if tc.partial {
					until = 1
				}
				if result.Until != until {
					t.Fatalf("window until = %v, want %v", result.Until, until)
				}
			}
			if source.read.Load() > 4096 {
				t.Fatalf("caption timing read media payload: %d bytes", source.read.Load())
			}
		})
	}
}

func TestCaptionHTTPOriginRetriesUnavailablePieces(t *testing.T) {
	source := captionOriginFixture(5000, false)
	handler := Handler(New(source), 0, true)
	source.blocked.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://host/?time=0", nil).WithContext(ctx))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable origin status = %d", response.Code)
	}
	source.blocked.Store(false)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://host/?time=0", nil))
	var result struct{ Cues []Cue }
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(result.Cues) != 1 || result.Cues[0] != (Cue{1, 3, "Caption"}) {
		t.Fatalf("retry lost normalized captions: status=%d cues=%+v", response.Code, result.Cues)
	}
}
