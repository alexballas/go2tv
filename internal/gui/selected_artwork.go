//go:build !(android || ios)

package gui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/mediaartwork"
	"go2tv.app/go2tv/v2/internal/mediamodel"
)

// selectedArtwork is UI-thread owned and independent of receiver metadata and
// playback state. Cancelling a selection also invalidates queued UI results.
type selectedArtwork struct {
	widget.BaseWidget
	content     *fyne.Container
	note        *canvas.Text
	picture     *canvas.Image
	placeholder *fyne.Container
	cancel      context.CancelFunc
}

func newSelectedArtwork() *selectedArtwork {
	icon := canvas.NewText("♪", color.Transparent)
	icon.TextSize = 42
	placeholder := container.NewCenter(icon)
	picture := canvas.NewImageFromImage(nil)
	picture.FillMode = canvas.ImageFillContain
	picture.CornerRadius = playbackSurfaceRadius
	picture.Hide()
	a := &selectedArtwork{
		content: container.NewStack(newPlaybackSurface(container.NewStack(placeholder, picture))),
		picture: picture, placeholder: placeholder,
		note: icon,
	}
	a.ExtendBaseWidget(a)
	return a
}

func (a *selectedArtwork) CreateRenderer() fyne.WidgetRenderer {
	r := &selectedArtworkRenderer{WidgetRenderer: widget.NewSimpleRenderer(a.content), card: a}
	r.Refresh()
	return r
}

type selectedArtworkRenderer struct {
	fyne.WidgetRenderer
	card *selectedArtwork
}

func (r *selectedArtworkRenderer) Refresh() {
	th := r.card.Theme()
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	r.card.note.Color = th.Color(theme.ColorNameForeground, variant)
	r.WidgetRenderer.Refresh()
}

func (a *selectedArtwork) selectMedia(request mediaartwork.Request) {
	if a.cancel != nil {
		a.cancel()
	}
	a.picture.Image = nil
	a.picture.Hide()
	a.placeholder.Show()
	if request.Path == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	a.cancel = cancel
	go func() {
		defer cancel()
		decoded := loadSelectedArtwork(ctx, request)
		if decoded == nil {
			return
		}
		fyne.DoAndWait(func() { a.applyArtwork(ctx, decoded) })
	}()
}

func loadSelectedArtwork(ctx context.Context, request mediaartwork.Request) image.Image {
	asset, err := mediaartwork.Resolve(ctx, request)
	if err != nil || asset == nil || ctx.Err() != nil {
		return nil
	}
	decoded, _, err := image.Decode(bytes.NewReader(asset.Data))
	if err != nil {
		return nil
	}
	return decoded
}

func (a *selectedArtwork) applyArtwork(ctx context.Context, decoded image.Image) {
	if ctx.Err() != nil {
		return
	}
	a.picture.Image = decoded
	a.picture.Show()
	a.picture.Refresh()
	a.placeholder.Hide()
}

func (s *FyneScreen) selectArtwork(mediaPath string) {
	if s.selectedArtwork == nil {
		return
	}
	s.selectedArtwork.selectMedia(mediaartwork.Request{Path: mediaPath, Kind: mediamodel.KindForPath(mediaPath), FFmpegPath: s.ffmpegPath})
}

// Keep a square thumbnail beside the controls inside the Playback card.
const playbackArtworkSize float32 = 96
const playbackArtworkGap float32 = 12

type artworkPlaybackLayout struct{}

func (artworkPlaybackLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	playback := objects[1].MinSize()
	side := fyne.Max(playbackArtworkSize, playback.Height)
	return fyne.NewSize(playback.Width+side+playbackArtworkGap, side)
}

func (artworkPlaybackLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	side := fyne.Max(playbackArtworkSize, objects[1].MinSize().Height)
	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(fyne.NewSize(side, side))
	objects[1].Move(fyne.NewPos(side+playbackArtworkGap, 0))
	objects[1].Resize(fyne.NewSize(size.Width-side-playbackArtworkGap, side))
}

var _ fyne.Layout = artworkPlaybackLayout{}
