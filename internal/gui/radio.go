package gui

import (
	"context"
	"fmt"

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
	radioCountryPreference   = "RadioCountry"
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
	countries       []radiobrowser.Country
	countryPicker   *radioCountryPicker
	countryCode     string
	selectPending   bool
	selected        int
	favoritesOnly   bool
	name            *widget.Entry
	country         *radioCountryButton
	status          *widget.Label
	details         *widget.Label
	list            *widget.List
	useStation      *widget.Button
	favorite        *widget.Button
	popular         *widget.Button
	favoritesButton *widget.Button
}

func radioWindow(ctx context.Context, screen *FyneScreen) fyne.CanvasObject {
	r := &radioView{
		screen: screen, parentCtx: ctx, ctx: ctx,
		artwork: screen.getRadioArtworkCache(), iconsPending: make(map[string]bool),
		client: radiobrowser.New("Go2TV/" + screen.version), selected: -1,
	}
	screen.radio = r
	r.loadRadioPreferences(fyne.CurrentApp().Preferences())
	r.name = widget.NewEntry()
	r.name.SetPlaceHolder(lang.L("Search station name or genre"))
	r.status = widget.NewLabel(lang.L("Discover stations from Radio Browser"))
	r.status.Wrapping = fyne.TextWrapWord
	r.details = widget.NewLabel(lang.L("Select a station"))
	r.details.Wrapping = fyne.TextWrapWord
	r.country = newRadioCountryButton(lang.L("Select country"), r.chooseCountry)
	if r.countryCode != "" {
		r.country.SetText(radioCountryName(r.countryCode))
	}
	r.list = widget.NewList(func() int { return len(r.stations) }, func() fyne.CanvasObject {
		return newRadioStationRow()
	}, func(id widget.ListItemID, object fyne.CanvasObject) {
		station := r.stations[id]
		asset, _, _ := r.artwork.lookup(station.Favicon)
		object.(*radioStationRow).setStation(station, r.isFavorite(station.UUID), asset)
		r.loadRadioIcon(station)
	})
	r.list.OnSelected = func(id widget.ListItemID) { r.selected = id; r.refreshRadioSelection() }
	r.useStation = widget.NewButtonWithIcon(lang.L("Use station"), theme.ConfirmIcon(), r.useRadioStation)
	r.useStation.Importance = widget.HighImportance
	r.favorite = widget.NewButtonWithIcon(lang.L("Save favorite"), theme.ContentAddIcon(), r.toggleFavorite)
	r.refreshRadioSelection()
	search := func() { r.favoritesOnly = false; r.searchRadioStations() }
	r.name.OnSubmitted = func(string) { search() }
	searchButton := widget.NewButtonWithIcon("", theme.SearchIcon(), search)
	r.popular = widget.NewButton(lang.L("Popular"), r.showPopular)
	r.favoritesButton = widget.NewButton(lang.L("Favorites"), r.showFavorites)
	filters := container.NewVBox(container.NewBorder(nil, nil, nil, searchButton, r.name), container.NewGridWithColumns(3, r.country, r.popular, r.favoritesButton), r.status)
	controls := container.NewVBox(r.details, container.NewGridWithColumns(2, r.favorite, r.useStation))
	r.content = container.NewPadded(container.NewBorder(filters, controls, nil, nil, r.list))
	return r.content
}

func (r *radioView) loadRadioDirectory() {
	r.refreshRadioSelection()
	if r.loaded {
		return
	}
	r.loaded = true
	if r.favoritesOnly {
		r.showFavorites()
	} else {
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

func (r *radioView) searchRadioStations() {
	if r.cancel != nil {
		r.cancel()
	}
	r.requestID++
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
				r.status.SetText(err.Error())
				return
			}
			r.setStations(stations)
			r.status.SetText(fmt.Sprintf(lang.L("Showing %d stations"), len(stations)))
		})
	}()
}

func (r *radioView) setStations(stations []radiobrowser.Station) {
	r.selected = -1
	r.list.UnselectAll()
	r.stations = stations
	r.list.Refresh()
	r.refreshRadioSelection()
}

func (r *radioView) refreshRadioSelection() {
	if r.selected < 0 || r.selected >= len(r.stations) {
		r.details.SetText(lang.L("Select a station"))
		r.useStation.Disable()
		r.favorite.Disable()
		return
	}
	station := r.stations[r.selected]
	detail := station.Name + "\n" + radioStationSummary(station)
	if station.Tags != "" {
		detail += "\n" + station.Tags
	}
	r.details.SetText(detail)
	if r.selectPending {
		r.useStation.Disable()
	} else {
		r.useStation.Enable()
	}
	r.favorite.Enable()
	if r.isFavorite(station.UUID) {
		r.favorite.SetText(lang.L("Remove favorite"))
	} else {
		r.favorite.SetText(lang.L("Save favorite"))
	}
}
