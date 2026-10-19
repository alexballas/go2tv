package gui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
)

const (
	radioFavoritesPreference = "RadioFavorites"
	radioRecentPreference    = "RadioRecent"
	radioCountryPreference   = "RadioCountry"
)

type radioListMode int

const (
	radioModeSearch radioListMode = iota
	radioModeFavorites
	radioModeRecent
)

type radioDirectory interface {
	SearchText(context.Context, string, string) ([]radiobrowser.Station, error)
	Countries(context.Context) ([]radiobrowser.Country, error)
	Station(context.Context, string) (radiobrowser.Station, error)
	Click(context.Context, string) error
}

// Dialog state and widgets are owned by the UI thread. Artwork fetches share
// a synchronized cache with playback startup and selected-station previews.
type radioView struct {
	screen          *FyneScreen
	parentCtx       context.Context
	ctx             context.Context
	modal           dialog.Dialog
	content         fyne.CanvasObject
	artwork         *radioArtworkCache
	iconsPending    map[string]bool
	client          radioDirectory
	loaded          bool
	cancel          context.CancelFunc
	requestID       uint64
	stations        []radiobrowser.Station
	favorites       []radiobrowser.Station
	recent          []radiobrowser.Station
	countries       []radiobrowser.Country
	countryPicker   *radioCountryPicker
	countryCode     string
	selectPending   bool
	selected        int
	mode            radioListMode
	name            *widget.Entry
	country         *radioCountryButton
	status          *widget.Label
	list            *widget.List
	useStation      *widget.Button
	favorite        *widget.Button
	popular         *widget.Button
	favoritesButton *widget.Button
	recentButton    *widget.Button
	lastTapID       widget.ListItemID
	lastTapAt       time.Time
}

func radioWindow(ctx context.Context, screen *FyneScreen) fyne.CanvasObject {
	r := &radioView{
		screen: screen, parentCtx: ctx, ctx: ctx,
		artwork: screen.getRadioArtworkCache(), iconsPending: make(map[string]bool),
		client: radiobrowser.New("Go2TV/" + screen.version), selected: -1, lastTapID: -1,
	}
	screen.radio = r
	r.loadRadioPreferences(fyne.CurrentApp().Preferences())
	r.name = widget.NewEntry()
	r.name.SetPlaceHolder(lang.L("Search station name or genre"))
	r.status = widget.NewLabel(lang.L("Discover stations from Radio Browser"))
	r.status.Wrapping = fyne.TextWrapWord
	r.country = newRadioCountryButton(lang.L("Select country"), r.chooseCountry)
	if r.countryCode != "" {
		r.country.SetText(radioCountryName(r.countryCode))
	}
	r.list = widget.NewList(func() int { return len(r.stations) }, func() fyne.CanvasObject {
		return newRadioStationRow(r.tapRadioRow)
	}, func(id widget.ListItemID, object fyne.CanvasObject) {
		station := r.stations[id]
		asset, _, _ := r.artwork.lookup(station.Icon())
		object.(*radioStationRow).setStation(id, station, r.isFavorite(station.UUID), r.screen.radioStationPlaying(station.UUID), asset)
		r.loadRadioIcon(station)
	})
	r.list.OnSelected = func(id widget.ListItemID) { r.selected = id; r.refreshRadioSelection() }
	r.list.OnReturn = r.useRadioRow
	r.useStation = widget.NewButtonWithIcon(lang.L("Use station"), theme.ConfirmIcon(), r.useRadioStation)
	r.useStation.Importance = widget.HighImportance
	r.favorite = widget.NewButton("☆ "+lang.L("Favorite"), r.toggleFavorite)
	r.refreshRadioSelection()
	search := func() { r.mode = radioModeSearch; r.searchRadioStations() }
	r.name.OnSubmitted = func(string) { search() }
	searchButton := widget.NewButtonWithIcon("", theme.SearchIcon(), search)
	r.popular = widget.NewButton(lang.L("Popular"), r.showPopular)
	r.favoritesButton = widget.NewButton(lang.L("Favorites"), r.showFavorites)
	r.recentButton = widget.NewButton(lang.L("Recent"), r.showRecent)
	filters := container.NewVBox(container.NewBorder(nil, nil, nil, searchButton, r.name), container.NewGridWithColumns(4, r.country, r.popular, r.favoritesButton, r.recentButton), r.status)
	// The highlighted row already describes the selection; a details panel
	// would repeat it and resize the list on every selection.
	controls := container.NewBorder(nil, nil, r.favorite, nil, r.useStation)
	r.content = container.NewPadded(container.NewBorder(filters, controls, nil, nil, r.list))
	return r.content
}

func (r *radioView) loadRadioDirectory() {
	r.refreshRadioSelection()
	if r.loaded {
		return
	}
	r.loaded = true
	switch r.mode {
	case radioModeFavorites:
		r.showFavorites()
	case radioModeRecent:
		r.showRecent()
	default:
		r.searchRadioStations()
	}
	r.loadRadioCountries()
}

func (r *radioView) loadRadioCountries() {
	ctx := r.ctx
	go func() {
		countries, err := r.client.Countries(ctx)
		fyne.Do(func() {
			if ctx.Err() != nil || r.ctx != ctx {
				return
			}
			if err != nil {
				r.status.SetText(lang.L("Countries unavailable; tap Country to retry"))
				return
			}
			r.setRadioCountries(countries)
		})
	}()
}

// tapRadioRow selects immediately; a second tap on the same row within the
// driver's double-tap delay uses the station.
func (r *radioView) tapRadioRow(id widget.ListItemID) {
	now := time.Now()
	double := id == r.lastTapID && now.Sub(r.lastTapAt) <= fyne.CurrentApp().Driver().DoubleTapDelay()
	r.lastTapID, r.lastTapAt = id, now
	if double {
		r.lastTapID = -1
		r.useRadioRow(id)
		return
	}
	r.list.Select(id)
}

// useRadioRow handles double-tap and Enter on a station row.
func (r *radioView) useRadioRow(id widget.ListItemID) {
	if id < 0 || id >= len(r.stations) {
		return
	}
	r.list.Select(id)
	r.useRadioStation()
}

// cancelRadioSearch drops in-flight directory results before the list changes.
func (r *radioView) cancelRadioSearch() {
	if r.cancel != nil {
		r.cancel()
	}
	r.requestID++
}

func (r *radioView) searchRadioStations() {
	r.cancelRadioSearch()
	id := r.requestID
	ctx, cancel := context.WithCancel(r.ctx)
	r.cancel = cancel
	query, country := r.name.Text, r.countryCode
	r.status.SetText(lang.L("Finding stations…"))
	go func() {
		stations, err := r.client.SearchText(ctx, query, country)
		cancel()
		fyne.Do(func() {
			if r.ctx.Err() != nil || id != r.requestID {
				return
			}
			if err != nil {
				r.screen.logRadioError("search", err)
				r.status.SetText(radioDirectoryError(err))
				return
			}
			r.setStations(stations)
			if len(stations) == 0 {
				r.status.SetText(lang.L("No stations found — try a genre or another country"))
				return
			}
			r.status.SetText(fmt.Sprintf(lang.L("Showing %d stations"), len(stations)))
		})
	}()
}

// radioDirectoryError turns transport failures into an actionable message;
// details still reach the debug log.
func radioDirectoryError(err error) string {
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return lang.L("Radio directory timed out; try again")
	case errors.Is(err, radiobrowser.ErrHTTPStatus):
		return lang.L("Radio directory unavailable; try again later")
	default:
		return lang.L("Can't reach the radio directory; check your connection")
	}
}

func (r *radioView) setStations(stations []radiobrowser.Station) {
	r.selected = -1
	r.lastTapID = -1
	r.list.UnselectAll()
	r.stations = stations
	r.list.Refresh()
	r.refreshRadioSelection()
}

func (r *radioView) refreshRadioSelection() {
	if r.selected < 0 || r.selected >= len(r.stations) {
		r.useStation.Disable()
		r.favorite.SetText("☆ " + lang.L("Favorite"))
		r.favorite.Disable()
		return
	}
	station := r.stations[r.selected]
	if r.selectPending {
		r.useStation.Disable()
	} else {
		r.useStation.Enable()
	}
	r.favorite.Enable()
	if r.isFavorite(station.UUID) {
		r.favorite.SetText("★ " + lang.L("Favorite"))
	} else {
		r.favorite.SetText("☆ " + lang.L("Favorite"))
	}
}
