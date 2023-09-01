package render

import (
	"bytes"
	"image"
	"image/draw"
	"io"

	"github.com/go-text/typesetting/opentype/api"
	"github.com/go-text/typesetting/shaping"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

func (r *Renderer) drawSVG(g shaping.Glyph, svg api.GlyphSVG, img draw.Image, x, y float32) error {
	pixWidth := int(fixed266ToFloat(g.Width) * r.PixScale)
	pixHeight := int(fixed266ToFloat(-g.Height) * r.PixScale)
	pix, err := renderSVGStream(bytes.NewReader(svg.Source), pixWidth, pixHeight)
	if err != nil {
		return err
	}

	rect := image.Rect(int(fixed266ToFloat(g.XBearing)*r.PixScale), int(fixed266ToFloat(-g.YBearing)*r.PixScale),
		pixWidth, pixHeight)
	draw.Draw(img, rect.Add(image.Point{X: int(x), Y: int(y)}), pix, image.Point{}, draw.Over)

	// ignore the svg.Outline shapes, as they are a fallback which we won't use
	return nil
}

func renderSVGStream(stream io.Reader, width, height int) (*image.NRGBA, error) {
	icon, err := oksvg.ReadIconStream(stream)
	if err != nil {
		return nil, err
	}

	iconAspect := float32(icon.ViewBox.W / icon.ViewBox.H)
	viewAspect := float32(width) / float32(height)
	imgW, imgH := width, height
	if viewAspect > iconAspect {
		imgW = int(float32(height) * iconAspect)
	} else if viewAspect < iconAspect {
		imgH = int(float32(width) / iconAspect)
	}

	icon.SetTarget(icon.ViewBox.X, icon.ViewBox.Y, float64(imgW), float64(imgH))

	out := image.NewNRGBA(image.Rect(0, 0, imgW, imgH))
	scanner := rasterx.NewScannerGV(int(icon.ViewBox.W), int(icon.ViewBox.H), out, out.Bounds())
	raster := rasterx.NewDasher(width, height, scanner)

	icon.Draw(raster, 1)
	return out, nil
}
