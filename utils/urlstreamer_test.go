package utils

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestStreamURLWithMimeHeaderType(t *testing.T) {
	body := []byte("stream-body")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(body)
	}))
	defer s.Close()

	stream, mediaType, err := StreamURLWithMime(context.Background(), s.URL)
	if err != nil {
		t.Fatalf("StreamURLWithMime failed: %v", err)
	}
	defer stream.Close()

	if mediaType != "video/mp4" {
		t.Fatalf("got mediaType %q, want %q", mediaType, "video/mp4")
	}

	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read stream: %v", err)
	}

	if !bytes.Equal(got, body) {
		t.Fatalf("stream body mismatch")
	}
}

func TestStreamURLWithMimeSniffFallback(t *testing.T) {
	body := videoBytes
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(body)
	}))
	defer s.Close()

	stream, mediaType, err := StreamURLWithMime(context.Background(), s.URL)
	if err != nil {
		t.Fatalf("StreamURLWithMime failed: %v", err)
	}
	defer stream.Close()

	if mediaType != "video/mp4" {
		t.Fatalf("got mediaType %q, want %q", mediaType, "video/mp4")
	}

	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read stream: %v", err)
	}

	if !bytes.Equal(got, body) {
		t.Fatalf("stream body mismatch")
	}
}

func TestStreamURLWithMimeHeaderWithParams(t *testing.T) {
	body := []byte("#EXTM3U\n#EXT-X-VERSION:3\n")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl; charset=utf-8")
		_, _ = w.Write(body)
	}))
	defer s.Close()

	stream, mediaType, err := StreamURLWithMime(context.Background(), s.URL)
	if err != nil {
		t.Fatalf("StreamURLWithMime failed: %v", err)
	}
	defer stream.Close()

	if mediaType != "application/vnd.apple.mpegurl" {
		t.Fatalf("got mediaType %q, want %q", mediaType, "application/vnd.apple.mpegurl")
	}

	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read stream: %v", err)
	}

	if !bytes.Equal(got, body) {
		t.Fatalf("stream body mismatch")
	}
}

func TestPrepareURLMediaStream(t *testing.T) {
	var requestCount int32
	body := []byte("stream-body")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(body)
	}))
	defer s.Close()

	mediaFile, mediaType, err := PrepareURLMedia(context.Background(), s.URL)
	if err != nil {
		t.Fatalf("PrepareURLMedia failed: %v", err)
	}

	stream, ok := mediaFile.(io.ReadCloser)
	if !ok {
		t.Fatalf("got type %T, want io.ReadCloser", mediaFile)
	}
	defer stream.Close()

	if mediaType != "video/mp4" {
		t.Fatalf("got mediaType %q, want %q", mediaType, "video/mp4")
	}

	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read stream: %v", err)
	}

	if !bytes.Equal(got, body) {
		t.Fatalf("stream body mismatch")
	}

	if atomic.LoadInt32(&requestCount) != 1 {
		t.Fatalf("got %d HTTP requests, want 1", requestCount)
	}
}

func TestPrepareURLMediaImageReturnsBytes(t *testing.T) {
	var requestCount int32
	body := []byte{0x89, 0x50, 0x4E, 0x47}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(body)
	}))
	defer s.Close()

	mediaFile, mediaType, err := PrepareURLMedia(context.Background(), s.URL)
	if err != nil {
		t.Fatalf("PrepareURLMedia failed: %v", err)
	}

	readerToBytes, ok := mediaFile.([]byte)
	if !ok {
		t.Fatalf("got type %T, want []byte", mediaFile)
	}

	if mediaType != "image/png" {
		t.Fatalf("got mediaType %q, want %q", mediaType, "image/png")
	}

	if !bytes.Equal(readerToBytes, body) {
		t.Fatalf("image bytes mismatch")
	}

	if atomic.LoadInt32(&requestCount) != 1 {
		t.Fatalf("got %d HTTP requests, want 1", requestCount)
	}
}
