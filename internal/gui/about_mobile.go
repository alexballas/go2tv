//go:build android || ios

package gui

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/widget"
)

func aboutWindow(s *FyneScreen) fyne.CanvasObject {
	richhead := widget.NewRichTextFromMarkdown(`
# Go2TV

Cast media files to Smart TVs

and Chromecast devices

---

## Author
Alex Ballas - alex@ballas.org

## License
MIT

## Version

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

	return container.NewVBox(richhead, container.NewCenter(container.NewHBox(githubbutton, checkversion)))
}

func checkVersion(s *FyneScreen) {
	fyne.Do(func() {
		s.CheckVersion.Disable()
	})
	defer fyne.Do(func() {
		s.CheckVersion.Enable()
	})
	errVersioncomp := errors.New(lang.L("failed to get version info") + "\n" + lang.L("you're using a development or a non-compiled version"))
	errVersionGet := errors.New(lang.L("failed to get version info") + "\n" + lang.L("check your internet connection"))

	// Parse current version
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
	}

	// We want to follow the redirects to the final URL which contains the version tag.
	client := &http.Client{
		Timeout: time.Duration(3 * time.Second),
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
