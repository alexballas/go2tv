package radiometadata

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func icyPacket(interval int, metadata string) []byte {
	data := bytes.Repeat([]byte{'a'}, interval)
	blocks := (len(metadata) + 15) / 16
	data = append(data, byte(blocks))
	data = append(data, metadata...)
	data = append(data, make([]byte, blocks*16-len(metadata))...)
	return data
}

func TestICYTitlesRespectIntervalsAndMetadataBlocks(t *testing.T) {
	data := append(icyPacket(4, "StreamTitle='Artist - First';"), icyPacket(4, "")...)
	data = append(data, icyPacket(4, "StreamTitle='Artist - First';")...)
	data = append(data, icyPacket(4, "StreamUrl='https://example.test';StreamTitle='Don\u0027t Stop';")...)
	data = append(data, icyPacket(4, "StreamTitle='';")...)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	var titles []string
	err := readTitles(ctx, cancel, bytes.NewReader(data), 4, readTimeout, func(title string) { titles = append(titles, title) })
	if !errors.Is(err, io.EOF) || !reflect.DeepEqual(titles, []string{"Artist - First", "Don't Stop", ""}) {
		t.Fatalf("ICY titles = %q, error %v", titles, err)
	}
}

func TestICYTruncatedAndOversizedIntervals(t *testing.T) {
	tt := []struct {
		name     string
		interval int
		data     []byte
	}{
		{name: "truncated metadata", interval: 4, data: []byte{'a', 'a', 'a', 'a', 2, 'x'}},
		{name: "zero interval", interval: 0},
		{name: "excessive interval", interval: MaxInterval + 1},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(context.Canceled)
			if err := readTitles(ctx, cancel, bytes.NewReader(tc.data), tc.interval, readTimeout, nil); err == nil {
				t.Fatal("malformed ICY stream accepted")
			}
		})
	}
}

func TestICYTimeoutMeasuresInactivity(t *testing.T) {
	tt := []struct {
		name   string
		active bool
	}{
		{name: "audio continues before metadata", active: true},
		{name: "stalled stream"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(context.Canceled)
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			stopClosing := context.AfterFunc(ctx, func() { _ = reader.CloseWithError(context.Cause(ctx)) })
			defer stopClosing()
			const interval = 8
			if tc.active {
				go func() {
					defer writer.Close()
					packet := icyPacket(interval, "StreamTitle='Still playing';")
					// The frame exceeds the idle limit; each read makes progress.
					for _, value := range packet[:interval] {
						select {
						case <-ctx.Done():
							return
						case <-time.After(100 * time.Millisecond):
						}
						if _, err := writer.Write([]byte{value}); err != nil {
							return
						}
					}
					_, _ = writer.Write(packet[interval:])
				}()
			}
			var titles []string
			err := readTitles(ctx, cancel, reader, interval, 500*time.Millisecond, func(title string) { titles = append(titles, title) })
			if tc.active {
				if !errors.Is(err, io.EOF) || !reflect.DeepEqual(titles, []string{"Still playing"}) {
					t.Fatalf("active stream titles = %q, error %v", titles, err)
				}
			} else if !errors.Is(err, context.DeadlineExceeded) || len(titles) != 0 {
				t.Fatalf("stalled stream titles = %q, error %v", titles, err)
			}
		})
	}
}

func TestICYHTTPConnectionCancelsAndCloses(t *testing.T) {
	started := make(chan struct{})
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Icy-MetaData") != "1" {
			t.Errorf("ICY metadata not requested: %v", r.Header)
		}
		w.Header().Set("icy-metaint", "4")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- StreamTitles(ctx, server.URL, nil) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ICY reader ignored cancellation")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("ICY side connection leaked")
	}
}

func TestNonICYStationDoesNotReadAudio(t *testing.T) {
	for _, interval := range []string{"", "0", "-1", "999999999"} {
		t.Run(interval, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("icy-metaint", interval)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := StreamTitles(ctx, server.URL, func(string) { t.Error("non-ICY station reported track") }); err != nil {
				t.Fatalf("non-ICY result = %v", err)
			}
		})
	}
	if title, _ := streamTitle("StreamTitle='" + strings.Repeat("x", 400) + "';"); len([]rune(title)) != maxTitleRunes {
		t.Fatal("track display length not bounded")
	}
}
