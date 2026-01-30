//go:build !(android || ios)

package gui

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
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
` + lang.L("Cast your media files to Smart TVs and Chromecast devices") + `

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
	githubbutton := widget.NewButton(lang.L("Website"), func() {
		go func() {
			u, _ := url.Parse("https://go2tv.app/")
			_ = fyne.CurrentApp().OpenURL(u)
		}()
	})
	checkversion := widget.NewButton(lang.L("Check version"), func() {
		go checkVersion(s)
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
	fyne.Do(func() {
		s.CheckVersion.Disable()
	})
	defer fyne.Do(func() {
		s.CheckVersion.Enable()
	})
	errVersioncomp := errors.New(lang.L("failed to get version info") + " - " + lang.L("you're using a development or a non-compiled version"))
	errVersionGet := errors.New(lang.L("failed to get version info") + " - " + lang.L("check your internet connection"))

	// Parse current version
	// We don't need to manually strip/replace, compareVersions handles it
	// But we do need to check if it's "dev" or invalid first to fail fast if we want,
	// or just let compareVersions return an error.
	// The original code did atoi check early.

	// Check if current version is valid/parsable (not "dev")
	if _, err := parseVersion(s.version); err != nil {
		fyne.Do(func() {
			dialog.ShowError(errVersioncomp, s.Current)
		})
		return
	}

	req, err := http.NewRequest("GET", "https://go2tv.app/latest", nil)
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(errVersionGet, s.Current)
		})
		return
	}

	// We want to follow the redirects to the final URL which contains the version tag.
	// client.Do handles following redirects by default up to a limit (10).
	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(errVersionGet, s.Current)
		})
		return
	}
	defer resp.Body.Close()

	latestVersionStr := filepath.Base(resp.Request.URL.Path)
	cmp, err := compareVersions(latestVersionStr, s.version)
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(errVersionGet, s.Current)
		})
		return
	}

	switch cmp {
	case 1:
		fyne.Do(func() {
			lbl := widget.NewLabel(lang.L("Current") + ": v" + s.version + " → " + lang.L("New") + ": " + latestVersionStr)
			lbl.Alignment = fyne.TextAlignCenter
			cnf := dialog.NewCustomConfirm(
				lang.L("Version checker"),
				lang.L("Download"),
				lang.L("Cancel"),
				lbl,
				func(b bool) {
					if b {
						u, _ := url.Parse("https://go2tv.app/latest")
						_ = fyne.CurrentApp().OpenURL(u)
					}
				},
				s.Current,
			)
			cnf.Show()
		})
		return
	default:
		fyne.Do(func() {
			dialog.ShowInformation(lang.L("Version checker"), lang.L("No new version"), s.Current)
		})
		return
	}
}
