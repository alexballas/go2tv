package radiobrowser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
			asset, err := FetchIcon(context.Background(), upstream.URL)
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
	if asset, err := FetchIcon(context.Background(), webpServer.URL); err != nil || asset.Width != 1 || asset.Height != 1 {
		t.Fatalf("WebP station logo = %+v, %v", asset, err)
	}
	if _, err := FetchIcon(context.Background(), "file:///tmp/logo.png"); err == nil {
		t.Fatal("local file favicon accepted")
	}
}
