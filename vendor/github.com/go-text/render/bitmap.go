package render

import (
	"bytes"
	"image"
	"image/draw"
	_ "image/jpeg" // load image formats for users of the API
	_ "image/png"

	"github.com/go-text/typesetting/shaping"
	scale "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff" // load image formats for users of the API

	"github.com/go-text/typesetting/opentype/api"
)

func (r *Renderer) drawBitmap(g shaping.Glyph, bitmap api.GlyphBitmap, img draw.Image, x, y float32) error {
	switch bitmap.Format {
	case api.BlackAndWhite:
		rec := image.Rect(0, 0, bitmap.Width, bitmap.Height)
		sub := image.NewNRGBA(rec)

		i := 0
		for y2 := 0; y2 < bitmap.Height; y2++ {
			for x2 := 0; x2 < bitmap.Width; x2 += 8 {
				v := bitmap.Data[i]

				if v&1 > 0 {
					sub.Set(x2+7, y2, r.Color)
				}
				if v&2 > 0 {
					sub.Set(x2+6, y2, r.Color)
				}
				if v&4 > 0 {
					sub.Set(x2+5, y2, r.Color)
				}
				if v&8 > 0 {
					sub.Set(x2+4, y2, r.Color)
				}
				if v&16 > 0 {
					sub.Set(x2+3, y2, r.Color)
				}
				if v&32 > 0 {
					sub.Set(x2+2, y2, r.Color)
				}
				if v&64 > 0 {
					sub.Set(x2+1, y2, r.Color)
				}
				if v&128 > 0 {
					sub.Set(x2, y2, r.Color)
				}
				i++
			}
		}

		rect := image.Rect(int(x), int(y), int(x+fixed266ToFloat(g.Width)*r.PixScale), int(y-fixed266ToFloat(g.Height)*r.PixScale))
		scale.NearestNeighbor.Scale(img, rect, sub, sub.Bounds(), draw.Over, nil)
	case api.JPG, api.PNG, api.TIFF:
		pix, _, err := image.Decode(bytes.NewReader(bitmap.Data))
		if err != nil {
			return err
		}

		rect := image.Rect(int(x), int(y), int(x+fixed266ToFloat(g.Width)*r.PixScale), int(y-fixed266ToFloat(g.Height)*r.PixScale))
		scale.BiLinear.Scale(img, rect, pix, pix.Bounds(), draw.Over, nil)
	}

	if bitmap.Outline != nil {
		r.drawOutline(g, *bitmap.Outline, r.filler, r.fillerScale, x, y)
	}
	return nil
}
