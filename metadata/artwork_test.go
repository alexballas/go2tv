package metadata

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexballas/tunetag/flac"
)

func TestLoadArtwork(t *testing.T) {
	jpegSmall := encodeTestImage(t, "jpeg", 320, 240, color.NRGBA{R: 220, A: 255})
	pngWide := encodeTestImage(t, "png", 1200, 600, color.NRGBA{G: 220, A: 255})
	gifData := encodeTestImage(t, "gif", 20, 10, color.NRGBA{B: 220, A: 255})

	tt := []struct {
		name       string
		data       []byte
		source     string
		wantWidth  int
		wantHeight int
		wantErr    error
	}{
		{
			name:       "JPEG no upscale",
			data:       jpegSmall,
			source:     "cover.png",
			wantWidth:  320,
			wantHeight: 240,
		},
		{
			name:       "PNG resize aspect ratio",
			data:       pngWide,
			source:     "cover.jpg",
			wantWidth:  600,
			wantHeight: 300,
		},
		{
			name:    "empty",
			wantErr: ErrArtworkEmpty,
		},
		{
			name:    "input size limit",
			data:    make([]byte, maxArtworkInputSize+1),
			wantErr: ErrArtworkTooLarge,
		},
		{
			name:    "pixel limit",
			data:    pngConfigOnly(t, 8000, 6000),
			wantErr: ErrArtworkPixels,
		},
		{
			name:    "unsupported image",
			data:    gifData,
			wantErr: ErrArtworkUnsupported,
		},
		{
			name:    "invalid bytes",
			data:    []byte("not an image"),
			wantErr: errors.New("decode error"),
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			asset, err := LoadArtwork(tc.data, tc.source)
			if tc.wantErr != nil {
				if err == nil {
					t.Fatal("expected error")
				}
				if tc.name != "invalid bytes" && !errors.Is(err, tc.wantErr) {
					t.Fatalf("got error %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if asset.MIMEType != "image/jpeg" || asset.Extension != ".jpg" {
				t.Fatalf("type = %q %q", asset.MIMEType, asset.Extension)
			}
			if asset.Width != tc.wantWidth || asset.Height != tc.wantHeight {
				t.Fatalf("dimensions = %dx%d, want %dx%d", asset.Width, asset.Height, tc.wantWidth, tc.wantHeight)
			}
			if asset.Source != tc.source {
				t.Fatalf("source = %q, want %q", asset.Source, tc.source)
			}
			sum := sha256.Sum256(asset.Data)
			if asset.ID != hex.EncodeToString(sum[:]) {
				t.Fatalf("ID = %q, want normalized-byte hash", asset.ID)
			}
			if asset.HandlerPath() != "/artwork/"+asset.ID+".jpg" {
				t.Fatalf("handler path = %q", asset.HandlerPath())
			}
			decoded, format, err := image.Decode(bytes.NewReader(asset.Data))
			if err != nil {
				t.Fatal(err)
			}
			if format != "jpeg" || decoded.Bounds().Dx() != tc.wantWidth || decoded.Bounds().Dy() != tc.wantHeight {
				t.Fatalf("output = %s %v", format, decoded.Bounds())
			}
		})
	}
}

func TestLoadArtworkFlattensTransparencyOntoBlack(t *testing.T) {
	input := encodeTestImage(t, "png", 8, 8, color.NRGBA{R: 255, G: 255, B: 255, A: 0})
	asset, err := LoadArtwork(input, "transparent.png")
	if err != nil {
		t.Fatal(err)
	}
	output, err := jpeg.Decode(bytes.NewReader(asset.Data))
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := output.At(4, 4).RGBA()
	if r > 0x0200 || g > 0x0200 || b > 0x0200 {
		t.Fatalf("transparent pixel = %#04x %#04x %#04x, want black", r, g, b)
	}
}

func TestLoadArtworkAppliesJPEGOrientation(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 16, 8))
	for y := range 8 {
		for x := range 16 {
			pixel := color.NRGBA{R: 255, A: 255}
			if x >= 8 {
				pixel = color.NRGBA{B: 255, A: 255}
			}
			source.SetNRGBA(x, y, pixel)
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	if got := jpegOrientation(encoded.Bytes()); got != 1 {
		t.Fatalf("untagged orientation = %d, want 1", got)
	}

	// APP1 EXIF segment: big-endian TIFF, IFD0 with a single orientation
	// entry set to 6 (stored rotated, display requires 90 degrees clockwise).
	exif := []byte{
		0xFF, 0xE1, 0x00, 0x22,
		'E', 'x', 'i', 'f', 0x00, 0x00,
		'M', 'M', 0x00, 0x2A, 0x00, 0x00, 0x00, 0x08,
		0x00, 0x01,
		0x01, 0x12, 0x00, 0x03, 0x00, 0x00, 0x00, 0x01, 0x00, 0x06, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}
	data := append(append(append([]byte(nil), encoded.Bytes()[:2]...), exif...), encoded.Bytes()[2:]...)
	if got := jpegOrientation(data); got != 6 {
		t.Fatalf("orientation = %d, want 6", got)
	}

	asset, err := LoadArtwork(data, "portrait.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if asset.Width != 8 || asset.Height != 16 {
		t.Fatalf("dimensions = %dx%d, want 8x16", asset.Width, asset.Height)
	}
	output, err := jpeg.Decode(bytes.NewReader(asset.Data))
	if err != nil {
		t.Fatal(err)
	}
	// The stored left half is red; rotated upright it becomes the top half.
	topR, _, topB, _ := output.At(4, 3).RGBA()
	bottomR, _, bottomB, _ := output.At(4, 12).RGBA()
	if topR < 0x8000 || topB > 0x8000 {
		t.Fatalf("top pixel = %#04x %#04x, want red", topR, topB)
	}
	if bottomR > 0x8000 || bottomB < 0x8000 {
		t.Fatalf("bottom pixel = %#04x %#04x, want blue", bottomR, bottomB)
	}
}

func TestLoadArtworkHashReuse(t *testing.T) {
	input := encodeTestImage(t, "png", 100, 50, color.NRGBA{R: 50, G: 80, B: 120, A: 255})
	first, err := LoadArtwork(input, "first.png")
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadArtwork(input, "second.png")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || !bytes.Equal(first.Data, second.Data) {
		t.Fatal("identical artwork did not reuse normalized hash")
	}
}

func TestResolveArtworkDiscovery(t *testing.T) {
	tt := []struct {
		name      string
		setup     func(*testing.T, string)
		want      string
		wantFound bool
	}{
		{
			name: "track stem wins case insensitively",
			setup: func(t *testing.T, dir string) {
				writeTestImage(t, filepath.Join(dir, "TRACK.PNG"), "png", 40, 40, color.NRGBA{R: 200, A: 255})
				writeTestImage(t, filepath.Join(dir, "cover.jpg"), "jpeg", 80, 80, color.NRGBA{B: 200, A: 255})
			},
			want:      "TRACK.PNG",
			wantFound: true,
		},
		{
			name: "corrupt candidates continue in named order",
			setup: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, "track.jpg"), []byte("bad"))
				writeTestFile(t, filepath.Join(dir, "cover.png"), []byte("bad"))
				writeTestImage(t, filepath.Join(dir, "FOLDER.JPEG"), "jpeg", 30, 30, color.NRGBA{G: 200, A: 255})
				writeTestImage(t, filepath.Join(dir, "front.jpg"), "jpeg", 50, 50, color.NRGBA{B: 200, A: 255})
			},
			want:      "FOLDER.JPEG",
			wantFound: true,
		},
		{
			name: "WMP large before small",
			setup: func(t *testing.T, dir string) {
				writeTestImage(t, filepath.Join(dir, "AlbumArt_guid_Large.PNG"), "png", 20, 20, color.NRGBA{R: 200, A: 255})
				writeTestImage(t, filepath.Join(dir, "albumart_guid_small.jpg"), "jpeg", 50, 50, color.NRGBA{B: 200, A: 255})
			},
			want:      "AlbumArt_guid_Large.PNG",
			wantFound: true,
		},
		{
			name: "WMP pattern anchored",
			setup: func(t *testing.T, dir string) {
				writeTestImage(t, filepath.Join(dir, "xalbumart_guid_large.jpg"), "jpeg", 80, 80, color.NRGBA{R: 200, A: 255})
				writeTestImage(t, filepath.Join(dir, "albumart_guid_small.png"), "png", 20, 20, color.NRGBA{B: 200, A: 255})
			},
			want:      "albumart_guid_small.png",
			wantFound: true,
		},
		{
			name: "no recursion or arbitrary image",
			setup: func(t *testing.T, dir string) {
				writeTestImage(t, filepath.Join(dir, "random.jpg"), "jpeg", 20, 20, color.NRGBA{R: 200, A: 255})
				nested := filepath.Join(dir, "nested")
				if err := os.Mkdir(nested, 0o755); err != nil {
					t.Fatal(err)
				}
				writeTestImage(t, filepath.Join(nested, "cover.png"), "png", 20, 20, color.NRGBA{B: 200, A: 255})
			},
		},
		{
			name: "track stem exact",
			setup: func(t *testing.T, dir string) {
				writeTestImage(t, filepath.Join(dir, "track-extra.jpg"), "jpeg", 20, 20, color.NRGBA{R: 200, A: 255})
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mediaPath := filepath.Join(dir, "track.WAV")
			writeTestFile(t, mediaPath, []byte("audio"))
			tc.setup(t, dir)

			asset, err := ResolveArtwork(mediaPath)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.wantFound {
				if asset != nil {
					t.Fatalf("found %q, want no artwork", asset.Source)
				}
				return
			}
			if asset == nil {
				t.Fatal("artwork not found")
			}
			if got := filepath.Base(asset.Source); got != tc.want {
				t.Fatalf("source = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveArtworkSameBasenameSelection(t *testing.T) {
	t.Run("largest pixel area", func(t *testing.T) {
		dir := t.TempDir()
		mediaPath := filepath.Join(dir, "track.wav")
		writeTestFile(t, mediaPath, []byte("audio"))
		writeTestImage(t, filepath.Join(dir, "cover.JPG"), "jpeg", 30, 30, color.NRGBA{R: 200, A: 255})
		writeTestImage(t, filepath.Join(dir, "COVER.jpeg"), "jpeg", 40, 30, color.NRGBA{G: 200, A: 255})
		writeTestImage(t, filepath.Join(dir, "Cover.PnG"), "png", 50, 30, color.NRGBA{B: 200, A: 255})

		asset, err := ResolveArtwork(mediaPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := filepath.Base(asset.Source); got != "Cover.PnG" {
			t.Fatalf("source = %q, want largest PNG", got)
		}
	})

	t.Run("extension tie break", func(t *testing.T) {
		dir := t.TempDir()
		mediaPath := filepath.Join(dir, "track.wav")
		writeTestFile(t, mediaPath, []byte("audio"))
		writeTestImage(t, filepath.Join(dir, "cover.jpg"), "jpeg", 30, 30, color.NRGBA{R: 200, A: 255})
		writeTestImage(t, filepath.Join(dir, "COVER.jpeg"), "jpeg", 30, 30, color.NRGBA{G: 200, A: 255})
		writeTestImage(t, filepath.Join(dir, "Cover.PNG"), "png", 30, 30, color.NRGBA{B: 200, A: 255})

		asset, err := ResolveArtwork(mediaPath)
		if err != nil {
			t.Fatal(err)
		}
		if got := filepath.Base(asset.Source); got != "cover.jpg" {
			t.Fatalf("source = %q, want .jpg tie winner", got)
		}
	})
}

func TestResolveArtworkEmbeddedFormats(t *testing.T) {
	invalid := []byte("invalid image")
	other := encodeTestImage(t, "png", 31, 17, color.NRGBA{R: 200, A: 255})
	front := encodeTestImage(t, "jpeg", 19, 29, color.NRGBA{B: 200, A: 255})
	frontAsset, err := LoadArtwork(front, "expected")
	if err != nil {
		t.Fatal(err)
	}
	otherAsset, err := LoadArtwork(other, "expected")
	if err != nil {
		t.Fatal(err)
	}

	tt := []struct {
		name      string
		extension string
		build     func([]embeddedPicture) []byte
		frontRole bool
	}{
		{name: "MP3 APIC", extension: ".mp3", build: buildTestMP3, frontRole: true},
		{name: "M4A covr", extension: ".m4a", build: buildTestMP4},
		{name: "MP4 covr", extension: ".mp4", build: buildTestMP4},
		{name: "FLAC PICTURE", extension: ".flac", build: buildTestFLAC, frontRole: true},
		{name: "Ogg Vorbis METADATA_BLOCK_PICTURE", extension: ".ogg", build: func(p []embeddedPicture) []byte { return buildTestOgg(t, false, p) }, frontRole: true},
		{name: "Opus METADATA_BLOCK_PICTURE", extension: ".opus", build: func(p []embeddedPicture) []byte { return buildTestOgg(t, true, p) }, frontRole: true},
	}

	for _, tc := range tt {
		t.Run(tc.name+" front preference", func(t *testing.T) {
			pictures := []embeddedPicture{
				{pictureType: 4, mime: "image/jpeg", data: invalid},
				{pictureType: 4, mime: "image/png", data: other},
			}
			wantID := otherAsset.ID
			if tc.frontRole {
				pictures = append(pictures, embeddedPicture{pictureType: 3, mime: "image/jpeg", data: front})
				wantID = frontAsset.ID
			}
			path := filepath.Join(t.TempDir(), "track"+tc.extension)
			writeTestFile(t, path, tc.build(pictures))

			asset, err := ResolveArtwork(path)
			if err != nil {
				t.Fatal(err)
			}
			if asset == nil || asset.ID != wantID {
				t.Fatalf("asset = %+v, want ID %s", asset, wantID)
			}
		})

		t.Run(tc.name+" first valid fallback", func(t *testing.T) {
			pictures := []embeddedPicture{
				{pictureType: 4, mime: "image/jpeg", data: invalid},
				{pictureType: 4, mime: "image/png", data: other},
				{pictureType: 4, mime: "image/jpeg", data: front},
			}
			path := filepath.Join(t.TempDir(), "track"+tc.extension)
			writeTestFile(t, path, tc.build(pictures))

			asset, err := ResolveArtwork(path)
			if err != nil {
				t.Fatal(err)
			}
			if asset == nil || asset.ID != otherAsset.ID {
				t.Fatalf("asset = %+v, want first valid ID %s", asset, otherAsset.ID)
			}
		})
	}
}

func TestResolveEmbeddedArtworkFromConfinedHandle(t *testing.T) {
	cover := encodeTestImage(t, "jpeg", 19, 29, color.NRGBA{B: 200, A: 255})
	want, err := LoadArtwork(cover, "track.mp3")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "track.mp3")
	writeTestFile(t, path, buildTestMP3([]embeddedPicture{{pictureType: 3, mime: "image/jpeg", data: cover}}))
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := ResolveEmbeddedArtwork(file, "track.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != want.ID {
		t.Fatalf("asset = %+v, want ID %s", got, want.ID)
	}
}

func TestResolveArtworkMissingDirectory(t *testing.T) {
	asset, err := ResolveArtwork(filepath.Join(t.TempDir(), "missing", "track.mp3"))
	if err != nil || asset != nil {
		t.Fatalf("asset = %+v, error = %v; want nil, nil", asset, err)
	}
}

type embeddedPicture struct {
	pictureType uint32
	mime        string
	data        []byte
}

func encodeTestImage(t *testing.T, format string, width, height int, fill color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, fill)
		}
	}
	var output bytes.Buffer
	var err error
	switch format {
	case "jpeg":
		err = jpeg.Encode(&output, img, &jpeg.Options{Quality: 92})
	case "png":
		err = png.Encode(&output, img)
	case "gif":
		err = gif.Encode(&output, img, nil)
	default:
		t.Fatalf("unsupported test format %q", format)
	}
	if err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func pngConfigOnly(t *testing.T, width, height uint32) []byte {
	t.Helper()
	var body bytes.Buffer
	_ = binary.Write(&body, binary.BigEndian, width)
	_ = binary.Write(&body, binary.BigEndian, height)
	body.Write([]byte{8, 2, 0, 0, 0})
	chunk := append([]byte("IHDR"), body.Bytes()...)
	var output bytes.Buffer
	output.Write([]byte("\x89PNG\r\n\x1a\n"))
	_ = binary.Write(&output, binary.BigEndian, uint32(body.Len()))
	output.Write(chunk)
	_ = binary.Write(&output, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return output.Bytes()
}

func writeTestImage(t *testing.T, path, format string, width, height int, fill color.NRGBA) {
	t.Helper()
	writeTestFile(t, path, encodeTestImage(t, format, width, height, fill))
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildTestMP3(pictures []embeddedPicture) []byte {
	var frames bytes.Buffer
	for _, picture := range pictures {
		var body bytes.Buffer
		body.WriteByte(0)
		body.WriteString(picture.mime)
		body.WriteByte(0)
		body.WriteByte(byte(picture.pictureType))
		body.WriteByte(0)
		body.Write(picture.data)

		frames.WriteString("APIC")
		_ = binary.Write(&frames, binary.BigEndian, uint32(body.Len()))
		frames.Write([]byte{0, 0})
		frames.Write(body.Bytes())
	}
	size := frames.Len()
	header := []byte{
		'I', 'D', '3', 3, 0, 0,
		byte(size >> 21 & 0x7f),
		byte(size >> 14 & 0x7f),
		byte(size >> 7 & 0x7f),
		byte(size & 0x7f),
	}
	return append(header, frames.Bytes()...)
}

func buildTestFLAC(pictures []embeddedPicture) []byte {
	var output bytes.Buffer
	output.WriteString("fLaC")
	output.Write([]byte{0, 0, 0, 34})
	output.Write(make([]byte, 34))
	for i, picture := range pictures {
		body, err := (&flac.Picture{
			PictureType: picture.pictureType,
			MIME:        picture.mime,
			Width:       1,
			Height:      1,
			Depth:       24,
			Data:        picture.data,
		}).Encode()
		if err != nil {
			panic(err)
		}
		blockType := byte(6)
		if i == len(pictures)-1 {
			blockType |= 0x80
		}
		output.WriteByte(blockType)
		output.Write([]byte{byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))})
		output.Write(body)
	}
	return output.Bytes()
}

func buildTestMP4(pictures []embeddedPicture) []byte {
	var covrBody bytes.Buffer
	for _, picture := range pictures {
		typeCode := uint32(13)
		if picture.mime == "image/png" {
			typeCode = 14
		}
		var data bytes.Buffer
		_ = binary.Write(&data, binary.BigEndian, typeCode)
		_ = binary.Write(&data, binary.BigEndian, uint32(0))
		data.Write(picture.data)
		covrBody.Write(mp4Box("data", data.Bytes()))
	}
	ilst := mp4Box("ilst", mp4Box("covr", covrBody.Bytes()))
	meta := mp4Box("meta", append([]byte{0, 0, 0, 0}, ilst...))
	moov := mp4Box("moov", mp4Box("udta", meta))
	ftyp := mp4Box("ftyp", []byte("M4A \x00\x00\x00\x00M4A mp42isom"))
	return append(ftyp, moov...)
}

func mp4Box(boxType string, body []byte) []byte {
	output := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(output[:4], uint32(len(output)))
	copy(output[4:8], boxType)
	copy(output[8:], body)
	return output
}

func buildTestOgg(t *testing.T, opus bool, pictures []embeddedPicture) []byte {
	t.Helper()
	comments := &flac.VorbisComment{Vendor: "go2tv-test"}
	for _, picture := range pictures {
		body, err := (&flac.Picture{
			PictureType: picture.pictureType,
			MIME:        picture.mime,
			Width:       1,
			Height:      1,
			Depth:       24,
			Data:        picture.data,
		}).Encode()
		if err != nil {
			t.Fatal(err)
		}
		comments.Add("METADATA_BLOCK_PICTURE", base64.StdEncoding.EncodeToString(body))
	}
	commentBody, err := comments.Encode()
	if err != nil {
		t.Fatal(err)
	}

	var identification, comment []byte
	if opus {
		identification = append([]byte("OpusHead"), make([]byte, 11)...)
		comment = append([]byte("OpusTags"), commentBody...)
	} else {
		identification = append([]byte{1}, []byte("vorbis")...)
		identification = append(identification, make([]byte, 23)...)
		comment = append([]byte{3}, []byte("vorbis")...)
		comment = append(comment, commentBody...)
		comment = append(comment, 1)
	}
	stream := buildTestOggPage(1, 0, 0x02, identification)
	return append(stream, buildTestOggPage(1, 1, 0, comment)...)
}

func buildTestOggPage(serial, sequence uint32, headerType byte, packets ...[]byte) []byte {
	var segments []byte
	var body bytes.Buffer
	for _, packet := range packets {
		remaining := len(packet)
		for remaining >= 255 {
			segments = append(segments, 255)
			remaining -= 255
		}
		segments = append(segments, byte(remaining))
		body.Write(packet)
	}
	var output bytes.Buffer
	output.WriteString("OggS")
	output.WriteByte(0)
	output.WriteByte(headerType)
	output.Write(make([]byte, 8))
	_ = binary.Write(&output, binary.LittleEndian, serial)
	_ = binary.Write(&output, binary.LittleEndian, sequence)
	_ = binary.Write(&output, binary.LittleEndian, uint32(0))
	output.WriteByte(byte(len(segments)))
	output.Write(segments)
	output.Write(body.Bytes())
	return output.Bytes()
}

func TestWindowsMediaPlayerPatterns(t *testing.T) {
	tt := []struct {
		base string
		size string
		want bool
	}{
		{base: "albumart_guid_large", size: "large", want: true},
		{base: "ALBUMART_x_LARGE", size: "large", want: true},
		{base: "albumart__large", size: "large"},
		{base: "xalbumart_guid_large", size: "large"},
		{base: "albumart_guid_large_extra", size: "large"},
		{base: "albumart_guid_small", size: "small", want: true},
	}
	for _, tc := range tt {
		t.Run(strings.Join([]string{tc.base, tc.size}, "/"), func(t *testing.T) {
			got := windowsMediaPlayerBases([]sidecarCandidate{{base: tc.base}}, tc.size)
			if (len(got) == 1) != tc.want {
				t.Fatalf("matches = %v, want %v", got, tc.want)
			}
		})
	}
}
