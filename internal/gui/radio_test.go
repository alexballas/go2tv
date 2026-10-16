package gui

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
)

func TestRadioMetadataFollowsSelectedURL(t *testing.T) {
	station := radiobrowser.Station{UUID: "radio-id", Name: "Radio Name", URL: "https://station.test/list.pls", ResolvedURL: "https://station.test/live.m3u8", Codec: "AAC", HLS: 1}
	screen := &FyneScreen{radioSelected: station}
	tt := []struct {
		name, url, inferred string
		transcode           bool
		wantType, wantTitle string
	}{
		{name: "HLS direct", url: station.StreamURL(), inferred: "application/vnd.apple.mpegurl", wantType: "application/vnd.apple.mpegurl", wantTitle: station.Name},
		{name: "HLS audio compatibility", url: station.StreamURL(), inferred: "application/vnd.apple.mpegurl", transcode: true, wantType: "audio/aac", wantTitle: station.Name},
		{name: "pasted video URL", url: "https://other.test/video.mp4", inferred: "video/mp4", transcode: true, wantType: "video/mp4"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := screen.radioMediaType(tc.url, tc.inferred, tc.transcode); got != tc.wantType {
				t.Fatalf("media type = %q, want %q", got, tc.wantType)
			}
			if got := screen.radioTitle(tc.url); got != tc.wantTitle {
				t.Fatalf("title = %q, want %q", got, tc.wantTitle)
			}
		})
	}
}

func TestRadioFavoritesPersistAndRemoveByUUID(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	screen := &FyneScreen{Current: app.NewWindow("Radio")}
	radioWindow(context.Background(), screen)
	station := radiobrowser.Station{UUID: "one", Name: "Station", URL: "https://station.test/live"}
	r := screen.radio
	r.setStations([]radiobrowser.Station{station})
	r.list.Select(0)
	r.toggleFavorite()
	restored := &FyneScreen{Current: app.NewWindow("Restored")}
	radioWindow(context.Background(), restored)
	if len(restored.radio.favorites) != 1 || restored.radio.favorites[0].UUID != station.UUID {
		t.Fatalf("restored favorites = %#v", restored.radio.favorites)
	}
	restored.radio.showFavorites()
	restored.radio.list.Select(0)
	restored.radio.toggleFavorite()
	if len(restored.radio.stations) != 0 || len(restored.radio.favorites) != 0 {
		t.Fatal("removed favorite still visible")
	}
}

func TestRadioControlsFitPhoneAndDesktopWidths(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("Radio")
	defer window.Close()
	screen := &FyneScreen{Current: window}
	content := radioWindow(context.Background(), screen)
	window.SetContent(content)
	screen.radio.country.SetText("United Kingdom of Great Britain and Northern Ireland")
	screen.radio.setStations([]radiobrowser.Station{{UUID: "one", Name: "Example Radio", Country: "United Kingdom", Codec: "AAC", Bitrate: 128, URL: "https://station.test/live"}})
	screen.radio.list.Select(0)
	tt := []struct {
		name      string
		width     float32
		favorited bool
	}{
		{name: "phone", width: 360},
		{name: "phone favorite", width: 360, favorited: true},
		{name: "desktop", width: 620},
		{name: "desktop favorite", width: 620, favorited: true},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			r := screen.radio
			r.favorites = nil
			if tc.favorited {
				r.favorites = append(r.favorites, r.stations[0])
			}
			r.refreshRadioSelection()
			window.Resize(fyne.NewSize(tc.width, 740))
			if content.MinSize().Width > tc.width {
				t.Fatalf("radio controls require width %v", content.MinSize().Width)
			}
			if screen.radio.list.Size().Height < 120 {
				t.Fatalf("station list collapsed: %v", screen.radio.list.Size())
			}
			filters := []fyne.CanvasObject{r.country, r.popular, r.favoritesButton, r.recentButton}
			for i := 1; i < len(filters); i++ {
				previous, current := filters[i-1], filters[i]
				if math.Abs(float64(previous.Size().Width-current.Size().Width)) > 0.5 || previous.Size().Height != current.Size().Height {
					t.Fatalf("filter button sizes differ: %v, %v", previous.Size(), current.Size())
				}
				previousPos, currentPos := app.Driver().AbsolutePositionForObject(previous), app.Driver().AbsolutePositionForObject(current)
				if previousPos.X+previous.Size().Width > currentPos.X || previousPos.Y != currentPos.Y {
					t.Fatal("Country, Popular, Favorites and Recent overlap or changed order")
				}
			}
			last := filters[len(filters)-1]
			if app.Driver().AbsolutePositionForObject(last).X+last.Size().Width > tc.width {
				t.Fatal("radio filter row overflows the window")
			}
		})
	}
}

func TestRadioCountryLabelsUseButtonWidth(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	button := newRadioCountryButton("Select country", nil)
	tt := []struct {
		name  string
		width float32
	}{
		{name: "Select country", width: 160},
		{name: "Greece", width: 160},
		{name: "United Kingdom", width: 280},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			button.SetText(tc.name)
			width := tc.width
			if width == 0 {
				width = button.MinSize().Width
			}
			button.Resize(fyne.NewSize(width, button.MinSize().Height))
			// Test the label's actual rendering area, not just its parent button.
			found := false
			for _, object := range test.WidgetRenderer(button).Objects() {
				label, ok := object.(*widget.RichText)
				if !ok {
					continue
				}
				found = true
				textWidth := fyne.MeasureText(tc.name, theme.SizeForWidget(theme.SizeNameText, button), fyne.TextStyle{Bold: true}).Width
				if label.Size().Width < textWidth {
					t.Fatalf("country label has width %.1f for %.1f of text in %.1f button", label.Size().Width, textWidth, width)
				}
				rendered := ""
				for _, object := range test.WidgetRenderer(label).Objects() {
					if text, ok := object.(*canvas.Text); ok && text.Visible() {
						rendered += text.Text
					}
				}
				if rendered != tc.name {
					t.Fatalf("country label rendered %q instead of %q", rendered, tc.name)
				}
			}
			if !found {
				t.Fatal("country label missing")
			}
		})
	}
}

func TestRadioCountryPickerRefreshesWhileOpen(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("Radio")
	defer window.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	screen := &FyneScreen{Current: window}
	radioWindow(ctx, screen)
	r := screen.radio
	r.chooseCountry()
	picker := r.countryPicker
	picker.search.SetText("gre")
	if picker.list.Length() != 1 {
		t.Fatal("unloaded picker should contain All countries")
	}
	// Country responses arrive after the dialog is already open.
	r.setRadioCountries([]radiobrowser.Country{{Name: "Germany", Code: "DE"}, {Name: "Greece", Code: "GR"}})
	if picker.list.Length() != 2 {
		t.Fatalf("open country picker did not update/filter: %d rows", picker.list.Length())
	}
	label := widget.NewLabel("")
	picker.list.UpdateItem(1, label)
	if label.Text != "Greece" {
		t.Fatalf("filtered country = %q", label.Text)
	}
	picker.list.Select(1)
	if r.countryCode != "GR" || app.Preferences().String("RadioCountry") != "GR" {
		t.Fatal("newly loaded country cannot be selected")
	}
	if r.countryPicker != nil {
		t.Fatal("closed picker retained")
	}
	r.chooseCountry()
	r.countryPicker.list.Select(0)
	if r.countryCode != "" || app.Preferences().String("RadioCountry") != "" || r.country.Text != "Select country" {
		t.Fatal("All countries did not clear country selection")
	}
}

func TestRadioUsesStreamAudioMIMEOverDirectoryCodec(t *testing.T) {
	station := radiobrowser.Station{UUID: "station", URL: "https://station.test/live", Codec: "MP3"}
	screen := &FyneScreen{radioSelected: station}
	tt := []struct {
		name, inferred string
		transcode      bool
		want           string
	}{
		{name: "direct AAC with stale MP3 metadata", inferred: "audio/aac", want: "audio/aac"},
		{name: "transcode actual Ogg", inferred: "audio/ogg", transcode: true, want: "audio/ogg"},
		{name: "unknown stream uses directory", inferred: "application/octet-stream", want: "audio/mpeg"},
		{name: "HLS transcode uses audio codec", inferred: "application/vnd.apple.mpegurl", transcode: true, want: "audio/mpeg"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			if got := screen.radioMediaType(station.StreamURL(), tc.inferred, tc.transcode); got != tc.want {
				t.Fatalf("media type = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRadioDirectHLSPreservesPlaylistOrigin(t *testing.T) {
	tt := []struct {
		name, url, mediaType string
		hls                  int
		transcode            bool
		direct               bool
	}{
		{name: "directory flag", url: "https://station.test/live", hls: 1, direct: true},
		{name: "stale directory flag with HLS MIME", url: "https://station.test/live", mediaType: "application/vnd.apple.mpegurl", direct: true},
		{name: "stale directory flag with playlist URL", url: "https://station.test/live.m3u8", direct: true},
		{name: "transcoded HLS uses local relay", url: "https://station.test/live.m3u8", transcode: true},
		{name: "MP3 uses local relay", url: "https://station.test/live", mediaType: "audio/mpeg"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			screen := &FyneScreen{radioSelected: radiobrowser.Station{UUID: "station", URL: tc.url, HLS: tc.hls}}
			mediaType := screen.radioMediaType(tc.url, tc.mediaType, tc.transcode)
			want := ""
			if tc.direct {
				want = tc.url
			}
			if got := screen.radioDirectHLSURL(tc.url, mediaType, tc.transcode); got != want {
				t.Fatalf("direct HLS URL = %q, want %q", got, want)
			}
			if got := screen.radioDirectHLSURL("https://other.test/live.m3u8", mediaType, false); got != "" {
				t.Fatalf("unselected stream used radio routing: %q", got)
			}
		})
	}
}

type radioTestDirectory struct {
	stations  []radiobrowser.Station
	countries []radiobrowser.Country
}

func (d radioTestDirectory) SearchText(ctx context.Context, _, _ string) ([]radiobrowser.Station, error) {
	return d.stations, ctx.Err()
}
func (d radioTestDirectory) Countries(ctx context.Context) ([]radiobrowser.Country, error) {
	return d.countries, ctx.Err()
}
func (d radioTestDirectory) Station(ctx context.Context, uuid string) (radiobrowser.Station, error) {
	if err := ctx.Err(); err != nil {
		return radiobrowser.Station{}, err
	}
	for _, station := range d.stations {
		if station.UUID == uuid {
			return station, nil
		}
	}
	return radiobrowser.Station{}, errors.New("station missing")
}
func (d radioTestDirectory) Click(context.Context, string) error { return nil }
