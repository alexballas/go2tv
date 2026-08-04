//go:build !(android || ios)

package gui

import (
	"image"
	"image/color"
	"net/url"
	"time"

	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	qrcode "github.com/yeqown/go-qrcode/v2"
)

const (
	remoteSessionQRCodeSize      = 220
	remoteSessionQRCodeQuietZone = 4
)

type remoteSessionQRCodeWriter struct {
	image image.Image
}

func (w *remoteSessionQRCodeWriter) Write(matrix qrcode.Matrix) error {
	width := matrix.Width() + 2*remoteSessionQRCodeQuietZone
	height := matrix.Height() + 2*remoteSessionQRCodeQuietZone
	bitmap := matrix.Bitmap()
	qrImage := image.NewPaletted(
		image.Rect(0, 0, remoteSessionQRCodeSize, remoteSessionQRCodeSize),
		color.Palette{color.White, color.Black},
	)
	for y := range remoteSessionQRCodeSize {
		moduleY := y*height/remoteSessionQRCodeSize - remoteSessionQRCodeQuietZone
		if moduleY < 0 || moduleY >= matrix.Height() {
			continue
		}
		for x := range remoteSessionQRCodeSize {
			moduleX := x*width/remoteSessionQRCodeSize - remoteSessionQRCodeQuietZone
			if moduleX >= 0 && moduleX < matrix.Width() && bitmap[moduleY][moduleX] {
				qrImage.SetColorIndex(x, y, 1)
			}
		}
	}
	w.image = qrImage
	return nil
}

func (*remoteSessionQRCodeWriter) Close() error {
	return nil
}

type remoteSessionStatusView struct {
	root        *fyne.Container
	desktopView fyne.CanvasObject
	statusView  fyne.CanvasObject
	headline    *widget.Label
	explanation *widget.Label
	copyButton  *widget.Button
	qrCode      *canvas.Image
	openButton  *widget.Button
	stopButton  *widget.Button
	url         string
	copyReset   *time.Timer
}

func newRemoteSessionStatusView(screen *FyneScreen, desktopView fyne.CanvasObject) *remoteSessionStatusView {
	headline := widget.NewLabelWithStyle(
		lang.L("Remote Web Session active"),
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)
	headline.SizeName = theme.SizeNameHeadingText

	explanation := widget.NewLabel(lang.L("Casting is controlled from the Web UI. This desktop session's playlist and playback are unavailable while the server is running."))
	explanation.Alignment = fyne.TextAlignCenter
	explanation.Wrapping = fyne.TextWrapWord

	copyButton := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), nil)
	copyButton.Importance = widget.LowImportance
	copyButton.Hide()

	qrCode := canvas.NewImageFromImage(nil)
	qrCode.FillMode = canvas.ImageFillContain
	qrCode.ScaleMode = canvas.ImageScalePixels
	qrCode.SetMinSize(fyne.NewSquareSize(remoteSessionQRCodeSize))
	qrCode.Hide()

	openButton := widget.NewButtonWithIcon(lang.L("Open Web UI"), theme.ComputerIcon(), func() {
		target := screen.remoteSession.Snapshot().URL
		if parsed, err := url.Parse(target); err == nil && target != "" {
			_ = fyne.CurrentApp().OpenURL(parsed)
		}
	})
	openButton.Importance = widget.HighImportance
	openButton.Disable()

	var stopButton *widget.Button
	stopButton = widget.NewButtonWithIcon(lang.L("Stop Session"), theme.MediaStopIcon(), func() {
		stopButton.Disable()
		screen.stopRemoteWebSession()
	})
	stopButton.Importance = widget.DangerImportance
	stopButton.Disable()

	statusContent := container.NewVBox(
		headline,
		explanation,
		container.NewCenter(qrCode),
		container.NewCenter(copyButton),
		container.NewCenter(container.NewHBox(openButton, stopButton)),
	)
	statusCard := widget.NewCard("", "", container.NewPadded(statusContent))
	statusView := container.NewCenter(statusCard)
	statusView.Hide()

	view := &remoteSessionStatusView{
		root:        container.NewStack(desktopView, statusView),
		desktopView: desktopView,
		statusView:  statusView,
		headline:    headline,
		explanation: explanation,
		copyButton:  copyButton,
		qrCode:      qrCode,
		openButton:  openButton,
		stopButton:  stopButton,
	}
	copyButton.OnTapped = view.copyURLToClipboard
	return view
}

func (v *remoteSessionStatusView) copyURLToClipboard() {
	if v.url == "" {
		return
	}
	fyne.CurrentApp().Clipboard().SetContent(v.url)
	v.copyButton.SetIcon(theme.ConfirmIcon())
	v.copyButton.SetText(lang.L("Copied to clipboard"))
	if v.copyReset != nil {
		v.copyReset.Stop()
	}
	v.copyReset = time.AfterFunc(2*time.Second, func() {
		fyne.Do(func() {
			if v.url != "" {
				v.copyButton.SetIcon(theme.ContentCopyIcon())
				v.copyButton.SetText(v.url)
			}
		})
	})
}

func (v *remoteSessionStatusView) apply(snapshot remoteSessionSnapshot, active bool) {
	if !active {
		v.statusView.Hide()
		v.desktopView.Show()
		return
	}

	v.desktopView.Hide()
	v.statusView.Show()
	v.updateURL(snapshot.URL)

	v.openButton.Disable()
	if snapshot.State == remoteSessionRunning && snapshot.URL != "" {
		v.openButton.Enable()
	}

	v.stopButton.Disable()
	if snapshot.State == remoteSessionStarting || snapshot.State == remoteSessionRunning {
		v.stopButton.Enable()
	}

	v.root.Refresh()
}

func (v *remoteSessionStatusView) updateURL(target string) {
	if target == v.url {
		return
	}
	v.url = target
	if v.copyReset != nil {
		v.copyReset.Stop()
	}
	if target == "" {
		v.copyButton.Hide()
		v.qrCode.Hide()
		return
	}

	v.copyButton.SetIcon(theme.ContentCopyIcon())
	v.copyButton.SetText(target)
	v.copyButton.Show()

	code, err := qrcode.NewWith(target, qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionMedium))
	if err != nil {
		v.qrCode.Hide()
		return
	}
	writer := &remoteSessionQRCodeWriter{}
	if err := code.Save(writer); err != nil {
		v.qrCode.Hide()
		return
	}
	v.qrCode.Image = writer.image
	v.qrCode.Refresh()
	v.qrCode.Show()
}

func (s *FyneScreen) bindRemoteSessionStatus() {
	if s.remoteSession == nil || s.remoteSessionStatus == nil {
		return
	}
	updates, done := s.remoteSession.Subscribe()
	s.remoteSessionUpdatesDone = done
	s.remoteSessionStatus.apply(<-updates, s.renderGate.remoteLeaseHeld())
	go func() {
		for snapshot := range updates {
			fyne.Do(func() {
				s.remoteSessionStatus.apply(snapshot, s.renderGate.remoteLeaseHeld())
			})
		}
	}()
}

func (s *FyneScreen) refreshRemoteSessionStatus() {
	if s.remoteSession == nil || s.remoteSessionStatus == nil {
		return
	}
	s.remoteSessionStatus.apply(s.remoteSession.Snapshot(), s.renderGate.remoteLeaseHeld())
}
