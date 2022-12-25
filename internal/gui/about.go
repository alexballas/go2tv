//go:build !(android || ios)
// +build !android,!ios

package gui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	fyne "fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage/repository"
	"fyne.io/fyne/v2/widget"
)

func aboutWindow(s *NewScreen) fyne.CanvasObject {
	var _ repository.Repository = (*DataRepository)(nil)
	repo := &DataRepository{}
	repository.Register("data", repo)

	richhead := widget.NewRichTextFromMarkdown(`
![Go2TV](data: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)

Cast your media files to UPnP/DLNA Media Renderers and Smart TVs

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
	githubbutton := widget.NewButton("Github page", func() {
		go func() {
			u, _ := url.Parse("https://github.com/alexballas/go2tv")
			_ = fyne.CurrentApp().OpenURL(u)
		}()
	})
	checkversion := widget.NewButton("Check version", func() {
		go checkVersion(s)
	})

	s.CheckVersion = checkversion

	return container.NewVBox(richhead, container.NewCenter(container.NewHBox(githubbutton, checkversion)))
}

func checkVersion(s *NewScreen) {
	s.CheckVersion.Disable()
	defer s.CheckVersion.Enable()
	errRedirectChecker := errors.New("redirect")
	errVersioncomp := errors.New("failed to get version info - on develop or non-compiled version")
	errVersionGet := errors.New("failed to get version info - check your internet connection")

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
		str := strings.Trim(filepath.Base(url.Path), "v")
		str = strings.ReplaceAll(str, ".", "")
		chversion, err := strconv.Atoi(str)
		if err != nil {
			dialog.ShowError(errVersionGet, s.Current)
			return
		}

		switch {
		case chversion > currversion:
			dialog.ShowInformation("Version checker", "New version: "+strings.Trim(filepath.Base(url.Path), "v"), s.Current)
			return
		default:
			dialog.ShowInformation("Version checker", "No new version", s.Current)
			return
		}
	}

	dialog.ShowError(errVersionGet, s.Current)
}

type DataRepository struct{}

func (r *DataRepository) Exists(u fyne.URI) (bool, error) {
	return true, nil
}
func (r *DataRepository) Reader(u fyne.URI) (fyne.URIReadCloser, error) {
	path := u.Path()

	if path == "" {
		return nil, fmt.Errorf("invalid path '%s'", path)
	}

	b64data := strings.TrimLeft(path, "/")
	b, err := base64.StdEncoding.DecodeString(b64data)
	if err != nil {
		return nil, err
	}

	return &dataReadCloser{data: b, uri: u}, nil
}

func (r *DataRepository) CanRead(u fyne.URI) (bool, error) {
	return true, nil
}

func (r *DataRepository) Destroy(string) {
}

type dataReadCloser struct {
	uri        fyne.URI
	data       []byte
	readCursor int
}

func (f *dataReadCloser) Close() error {
	return nil
}

func (f *dataReadCloser) Read(p []byte) (int, error) {
	count := 0
	for ; count < len(p) && f.readCursor < len(f.data); f.readCursor++ {
		p[count] = f.data[f.readCursor]
		count++
	}

	var err error = nil
	if f.readCursor >= len(f.data) {
		err = io.EOF
	}

	return count, err
}

func (f *dataReadCloser) URI() fyne.URI {
	return f.uri
}
