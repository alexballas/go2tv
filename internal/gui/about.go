//go:build !(android || ios)

package gui

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/widget"
)

func aboutWindow(s *FyneScreen) fyne.CanvasObject {
	sr := fyne.NewStaticResource("Go2TV Icon", go2TVIcon512)
	go2tvImage := canvas.NewImageFromResource(sr)
	richhead := widget.NewRichTextFromMarkdown(`
` + lang.L("Cast your media files to UPnP/DLNA Media Renderers and Smart TVs") + `

---

## ` + lang.L("Author") + `
Alex Ballas - alex@ballas.org

## ` + lang.L("License") + `
MIT

## ` + lang.L("Version") + `

` + s.version)

	for i := range richhead.Segments {
		if seg, ok := richhead.Segments[i].(*widget.TextSegment); ok {
			seg.Style.Alignment = fyne.TextAlignCenter
		}
		if seg, ok := richhead.Segments[i].(*widget.HyperlinkSegment); ok {
			seg.Alignment = fyne.TextAlignCenter
		}
	}
	githubbutton := widget.NewButton(lang.L("Github page"), func() {
		go func() {
			u, _ := url.Parse("https://github.com/alexballas/go2tv")
			_ = fyne.CurrentApp().OpenURL(u)
		}()
	})
	checkversion := widget.NewButton(lang.L("Check version"), func() {
		go fyne.Do(func() {
			checkVersion(s)
		})
	})

	s.CheckVersion = checkversion
	go2tvImage.SetMinSize(fyne.Size{
		Width:  64,
		Height: 64,
	})

	imageContainer := container.NewCenter(go2tvImage)
	cont := container.NewVBox(
		imageContainer,
		container.NewCenter(richhead),
		container.NewCenter(container.NewHBox(githubbutton, checkversion)))
	return container.NewPadded(cont)
}

func checkVersion(s *FyneScreen) {
	s.CheckVersion.Disable()
	defer s.CheckVersion.Enable()
	errRedirectChecker := errors.New("redirect")
	errVersioncomp := errors.New(lang.L("failed to get version info") + " - " + lang.L("you're using a development or a non-compiled version"))
	errVersionGet := errors.New(lang.L("failed to get version info") + " - " + lang.L("check your internet connection"))

	str := strings.ReplaceAll(s.version, ".", "")
	str = strings.TrimSpace(str)
	currversion, err := strconv.Atoi(str)
	if err != nil {
		dialog.ShowError(errVersioncomp, s.Current)
		return
	}

	req, err := http.NewRequest("GET", "https://github.com/alexballas/Go2TV/releases/latest", nil)
	if err != nil {
		dialog.ShowError(errVersionGet, s.Current)
	}

	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errRedirectChecker
		},
	}

	response, err := client.Do(req)
	if err != nil && !errors.Is(err, errRedirectChecker) {
		dialog.ShowError(errVersionGet, s.Current)
		return
	}

	defer response.Body.Close()

	if errors.Is(err, errRedirectChecker) {
		responceUrl, err := response.Location()
		if err != nil {
			dialog.ShowError(errVersionGet, s.Current)
			return
		}
		str := strings.Trim(filepath.Base(responceUrl.Path), "v")
		str = strings.ReplaceAll(str, ".", "")
		chversion, err := strconv.Atoi(str)
		if err != nil {
			dialog.ShowError(errVersionGet, s.Current)
			return
		}

		switch {
		case chversion > currversion:
			dialog.ShowInformation(lang.L("Version checker"), lang.L("New version")+": "+strings.Trim(filepath.Base(responceUrl.Path), "v"), s.Current)
			return
		default:
			dialog.ShowInformation(lang.L("Version checker"), lang.L("No new version"), s.Current)
			return
		}
	}

	dialog.ShowError(errVersionGet, s.Current)
}
