//go:build android || ios

package gui

import (
	"errors"
	"net/url"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/widget"
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
	copyDiagnostics := widget.NewButton(lang.L("Copy Diagnostics"), func() {
		copyDiagnosticsToClipboard(s)
	})

	s.CheckVersion = checkversion

	return container.NewVBox(
		richhead,
		container.NewCenter(container.NewHBox(githubbutton, checkversion)),
		container.NewCenter(copyDiagnostics),
	)
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

	if !validVersion(s.version) {
		fyne.Do(func() {
			dialog.ShowError(errVersioncomp, s.Current)
		})
		return
	}

	latestVersionStr, err := getLatestVersion()
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(errVersionGet, s.Current)
		})
		return
	}

	cmp, err := compareVersions(latestVersionStr, s.version)
	if err != nil {
		fyne.Do(func() {
			dialog.ShowError(errVersionGet, s.Current)
		})
		return
	}

	switch cmp {
	case 1:
		showVersionPopup(latestVersionStr, s)
		return
	default:
		fyne.Do(func() {
			dialog.ShowInformation(lang.L("Version checker"), lang.L("No new version"), s.Current)
		})
		return
	}
}
