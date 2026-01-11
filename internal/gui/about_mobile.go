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

	return container.NewVBox(richhead, container.NewCenter(container.NewHBox(githubbutton, checkversion)))
}

func checkVersion(s *FyneScreen) {
	s.CheckVersion.Disable()
	defer s.CheckVersion.Enable()
	errRedirectChecker := errors.New("redirect")
	errVersioncomp := errors.New(lang.L("failed to get version info") + "\n" + lang.L("you're using a development or a non-compiled version"))
	errVersionGet := errors.New(lang.L("failed to get version info") + "\n" + lang.L("check your internet connection"))

	// Parse current version
	// Check if current version is valid/parsable (not "dev")
	if _, err := parseVersion(s.version); err != nil {
		dialog.ShowError(errVersioncomp, s.Current)
		return
	}

	req, err := http.NewRequest("GET", "https://github.com/alexballas/Go2TV/releases/latest", nil)
	if err != nil {
		dialog.ShowError(errVersionGet, s.Current)
	}

	client := &http.Client{
		Timeout: time.Duration(3 * time.Second),
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
		url, err := response.Location()
		if err != nil {
			dialog.ShowError(errVersionGet, s.Current)
			return
		}

		latestVersionStr := filepath.Base(url.Path)

		cmp, err := compareVersions(latestVersionStr, s.version)
		if err != nil {
			dialog.ShowError(errVersionGet, s.Current)
			return
		}

		switch {
		case cmp == 1: // latest > current
			dialog.ShowInformation(lang.L("Version checker"), lang.L("New version")+": "+latestVersionStr, s.Current)
			return
		default:
			dialog.ShowInformation(lang.L("Version checker"), lang.L("No new version"), s.Current)
			return
		}
	}

	dialog.ShowError(errVersionGet, s.Current)
}
