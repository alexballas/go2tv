package gui

import (
	"context"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/widget"
)

func newRadioButton(screen *FyneScreen) *widget.Button {
	return widget.NewButton(lang.L("Radio…"), func() { showRadioDialog(screen) })
}

func showRadioDialog(screen *FyneScreen) {
	if screen.radio != nil && screen.radio.modal != nil {
		screen.radio.modal.Show()
		return
	}
	if screen.radio == nil {
		ctx := screen.torrent.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		radioWindow(ctx, screen)
	}
	r := screen.radio
	ctx, cancel := context.WithCancel(r.parentCtx)
	r.ctx = ctx
	r.iconsPending = make(map[string]bool)
	r.loaded = false
	resumeHotkeys := suspendHotkeys(screen)
	r.modal = dialog.NewCustom(lang.L("Internet radio"), lang.L("Close"), r.content, screen.Current)
	r.modal.SetOnClosed(func() {
		cancel()
		r.requestID++
		r.selectPending = false
		r.selected = -1
		r.list.UnselectAll()
		r.refreshRadioSelection()
		if r.countryPicker != nil {
			r.countryPicker.popup.Hide()
		}
		r.modal = nil
		resumeHotkeys()
	})
	size := screen.Current.Canvas().Size()
	width, height := float32(620), float32(560)
	if size.Width >= 280 {
		width = min(width, size.Width-24)
	}
	if size.Height >= 360 {
		height = min(height, size.Height-32)
	}
	r.modal.Resize(fyne.NewSize(width, height))
	r.modal.Show()
	r.loadRadioDirectory()
}
