package render

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg" // load image formats for users of the API
	_ "image/png"

	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/shaping"
	scale "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff" // load image formats for users of the API
)

func (r *Renderer) drawBitmap(g shaping.Glyph, bitmap font.GlyphBitmap, img draw.Image, x, y float32) error {
	// scaled glyph rect content
	top := y - fixed266ToFloat(g.YBearing)*r.PixScale
	bottom := top - fixed266ToFloat(g.Height)*r.PixScale
	right := x + fixed266ToFloat(g.Width)*r.PixScale
	switch bitmap.Format {
	case font.BlackAndWhite:
		rec := image.Rect(0, 0, bitmap.Width, bitmap.Height)
		sub := image.NewPaletted(rec, color.Palette{color.Transparent, r.Color})

		for i := range sub.Pix {
			sub.Pix[i] = bitAt(bitmap.Data, i)
		}

		rect := image.Rect(int(x), int(top), int(right), int(bottom))
		scale.NearestNeighbor.Scale(img, rect, sub, sub.Bounds(), draw.Over, nil)
	case font.JPG, font.PNG, font.TIFF:
		pix, _, err := image.Decode(bytes.NewReader(bitmap.Data))
		if err != nil {
			return err
		}

		rect := image.Rect(int(x), int(top), int(right), int(bottom))
		scale.BiLinear.Scale(img, rect, pix, pix.Bounds(), draw.Over, nil)
	}

	if bitmap.Outline != nil {
		r.drawOutline(g, *bitmap.Outline, r.filler, r.fillerScale, x, y)
	}
	return nil
}

// bitAt returns the bit at the given index in the byte slice.
func bitAt(b []byte, i int) byte {
	return (b[i/8] >> (7 - i%8)) & 1
}
