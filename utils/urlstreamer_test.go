package utils

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
