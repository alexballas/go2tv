package playlists

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestParsePLSFormat(t *testing.T) {
	var wantUrls = []struct {
		url   string
		title string
	}{
		{"https://ice4.somafm.com/indiepop-64-aac", "SomaFM: Indie Pop Rocks! (#1): New and classic favorite indie pop tracks."},
		{"https://ice2.somafm.com/indiepop-64-aac", "SomaFM: Indie Pop Rocks! (#2): New and classic favorite indie pop tracks."},
		{"https://ice1.somafm.com/indiepop-64-aac", "SomaFM: Indie Pop Rocks! (#3): New and classic favorite indie pop tracks."},
		{"https://ice6.somafm.com/indiepop-64-aac", "SomaFM: Indie Pop Rocks! (#4): New and classic favorite indie pop tracks."},
		{"https://ice5.somafm.com/indiepop-64-aac", "SomaFM: Indie Pop Rocks! (#5): New and classic favorite indie pop tracks."},
	}
	var path string
	if abs, err := filepath.Abs(filepath.Join("testdata", "indiepop64.pls")); err != nil {
		t.Fatal(err)
	} else {
		path = fmt.Sprintf("file://%v", abs)
	}
	it, err := newPLSIterator(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range wantUrls {
		if !it.HasNext() {
			t.Fatal("iterator exhausted")
		}
		url, title := it.Next()
		if url != want.url {
			t.Fatalf("test %d, have url %v want %v", i, url, want.url)
		}
		if title != want.title {
			t.Fatalf("test %d, have title %v want %v", i, title, want.title)
		}
	}
	if it.HasNext() {
		t.Fatal("expected exhausted iterator")
	}
}

func TestParseM3UFormat(t *testing.T) {
	var wantUrls = []struct {
		url   string
		title string
	}{
		{"http://ice1.somafm.com/indiepop-128-aac", ""},
		{"http://ice4.somafm.com/indiepop-128-aac", ""},
		{"http://ice2.somafm.com/indiepop-128-aac", ""},
		{"http://ice6.somafm.com/indiepop-128-aac", ""},
		{"http://ice5.somafm.com/indiepop-128-aac", ""},
	}
	var path string
	if abs, err := filepath.Abs(filepath.Join("testdata", "indiepop130.m3u")); err != nil {
		t.Fatal(err)
	} else {
		path = fmt.Sprintf("file://%v", abs)
	}
	it, err := newM3UIterator(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range wantUrls {
		if !it.HasNext() {
			t.Fatal("iterator exhausted")
		}
		url, title := it.Next()
		if url != want.url {
			t.Fatalf("test %d, have url %v want %v", i, url, want.url)
		}
		if title != want.title {
			t.Fatalf("test %d, have title %v want %v", i, title, want.title)
		}
	}
	if it.HasNext() {
		t.Fatal("expected exhausted iterator")
	}
}
