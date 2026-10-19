package radiobrowser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fyne-io/image/ico"
)

func TestFetchIconIsBoundedAndNormalized(t *testing.T) {
	var pngData, gifData bytes.Buffer
	if err := png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 24, 16))); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&gifData, image.NewPaletted(image.Rect(0, 0, 24, 16), color.Palette{color.Black, color.White}), nil); err != nil {
		t.Fatal(err)
	}
	valid := pngData.Bytes()
	largePixels := append([]byte(nil), valid...)
	binary.BigEndian.PutUint32(largePixels[16:20], 4096)
	binary.BigEndian.PutUint32(largePixels[20:24], 4096)
	binary.BigEndian.PutUint32(largePixels[29:33], crc32.ChecksumIEEE(largePixels[12:29]))
	tt := []struct {
		name   string
		body   []byte
		status int
		want   bool
	}{
		{name: "PNG station logo", body: valid, status: 200, want: true},
		{name: "GIF station logo", body: gifData.Bytes(), status: 200, want: true},
		{name: "SVG fallback", body: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), status: 200},
		{name: "oversized body", body: []byte(strings.Repeat("x", MaxIconBytes+1)), status: 200},
		{name: "pixel limit", body: largePixels, status: 200},
		{name: "missing icon", status: 404},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write(tc.body) }))
			defer upstream.Close()
			asset, err := FetchIcon(context.Background(), IconSource{Favicon: upstream.URL})
			if (err == nil && asset != nil) != tc.want {
				t.Fatalf("icon result = %v, %v", asset, err)
			}
			if tc.want && (asset.MIMEType != "image/jpeg" || asset.Width != 24 || asset.Height != 16) {
				t.Fatalf("normalized icon = %+v", asset)
			}
		})
	}
	webp, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatal(err)
	}
	webpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(webp) }))
	defer webpServer.Close()
	if asset, err := FetchIcon(context.Background(), IconSource{Favicon: webpServer.URL}); err != nil || asset.Width != 1 || asset.Height != 1 {
		t.Fatalf("WebP station logo = %+v, %v", asset, err)
	}
	if _, err := FetchIcon(context.Background(), IconSource{Favicon: "file:///tmp/logo.png"}); err == nil {
		t.Fatal("local file favicon accepted")
	}
}

func TestFetchIconFallsBackToHomepageIcon(t *testing.T) {
	encode := func(width int, encoder func(io.Writer, image.Image) error) []byte {
		var data bytes.Buffer
		if err := encoder(&data, image.NewRGBA(image.Rect(0, 0, width, width))); err != nil {
			t.Fatal(err)
		}
		return data.Bytes()
	}
	files := map[string][]byte{
		"/logo.png":    encode(32, png.Encode),
		"/small.ico":   encode(48, ico.Encode),
		"/favicon.ico": encode(64, ico.Encode),
		"/logo.svg":    []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`),
		// Dead favicons often answer with a 200 HTML page.
		"/missing.png": []byte("<html>not found</html>"),
	}
	tt := []struct {
		name      string
		favicon   string
		head      string
		wantWidth int
	}{
		{name: "no favicon uses declared PNG", head: `<link rel="icon" href="/small.ico"><link rel="apple-touch-icon" sizes="180x180" href="/logo.png">`, wantWidth: 32},
		{name: "dead favicon uses declared icon", favicon: "/missing.png", head: `<link rel="shortcut icon" href="small.ico">`, wantWidth: 48},
		{name: "no declared icon uses favicon.ico", wantWidth: 64},
		{name: "SVG declaration skipped", head: `<link rel="icon" type="image/svg+xml" href="/logo.svg">`, wantWidth: 64},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" {
					w.Write([]byte("<html><head><title>Station</title>" + tc.head + "</head><body></body></html>"))
					return
				}
				data, ok := files[r.URL.Path]
				if !ok {
					http.NotFound(w, r)
					return
				}
				w.Write(data)
			}))
			defer upstream.Close()
			source := IconSource{Homepage: upstream.URL + "/"}
			if tc.favicon != "" {
				source.Favicon = upstream.URL + tc.favicon
			}
			asset, err := FetchIcon(context.Background(), source)
			if err != nil || asset.Width != tc.wantWidth {
				t.Fatalf("icon = %+v, %v; want width %d", asset, err, tc.wantWidth)
			}
		})
	}
}

func TestFetchIconRejectsMalformedICO(t *testing.T) {
	var logo bytes.Buffer
	if err := png.Encode(&logo, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	ico := func(sizes []uint32, body []byte) []byte {
		data := binary.LittleEndian.AppendUint16([]byte{0, 0, 1, 0}, uint16(len(sizes)))
		for _, size := range sizes {
			data = append(data, 1, 1, 0, 0, 1, 0, 32, 0)
			data = binary.LittleEndian.AppendUint32(data, size)
			data = binary.LittleEndian.AppendUint32(data, 0)
		}
		return append(data, body...)
	}
	tt := []struct {
		name string
		body []byte
	}{
		// Passes DecodeConfig via the first entry, then panics on the second.
		{name: "truncated bitmap entry", body: ico([]uint32{uint32(logo.Len()), 8}, append(logo.Bytes(), make([]byte, 8)...))},
		{name: "entry larger than file", body: ico([]uint32{0xfffffff0}, logo.Bytes())},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(tc.body) }))
			defer upstream.Close()
			if asset, err := FetchIcon(context.Background(), IconSource{Favicon: upstream.URL}); err == nil {
				t.Fatalf("malformed ICO accepted: %+v", asset)
			}
		})
	}
}

func TestFetchIconCanceledHomepageOnlyFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	asset, err := FetchIcon(ctx, IconSource{Homepage: "https://station.test/"})
	if !errors.Is(err, context.Canceled) || asset != nil {
		t.Fatalf("canceled fetch = %v, %v", asset, err)
	}
}
