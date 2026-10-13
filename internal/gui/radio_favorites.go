package gui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/lang"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
)

func (r *radioView) loadRadioPreferences(prefs fyne.Preferences) {
	r.countryCode = prefs.String(radioCountryPreference)
	data := prefs.String(radioFavoritesPreference)
	if data == "" {
		return
	}
	if err := json.Unmarshal([]byte(data), &r.favorites); err != nil {
		r.favorites = nil
		r.screen.logRadioError("load favorites", err)
	}
}

func (r *radioView) showFavorites() {
	if r.cancel != nil {
		r.cancel()
	}
	r.requestID++
	r.favoritesOnly = true
	stations := append([]radiobrowser.Station(nil), r.favorites...)
	sort.Slice(stations, func(i, j int) bool { return strings.ToLower(stations[i].Name) < strings.ToLower(stations[j].Name) })
	r.setStations(stations)
	r.status.SetText(fmt.Sprintf(lang.L("Saved %d favorites"), len(stations)))
}

func (r *radioView) showPopular() {
	r.name.SetText("")
	r.favoritesOnly = false
	r.searchRadioStations()
}

func (r *radioView) isFavorite(uuid string) bool {
	for _, station := range r.favorites {
		if station.UUID == uuid {
			return true
		}
	}
	return false
}

func (r *radioView) toggleFavorite() {
	if r.selected < 0 || r.selected >= len(r.stations) {
		return
	}
	station := r.stations[r.selected]
	found := false
	for i, favorite := range r.favorites {
		if favorite.UUID == station.UUID {
			r.favorites = append(r.favorites[:i], r.favorites[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		r.favorites = append(r.favorites, station)
	}
	data, err := json.Marshal(r.favorites)
	if err != nil {
		r.status.SetText(err.Error())
		return
	}
	fyne.CurrentApp().Preferences().SetString(radioFavoritesPreference, string(data))
	if r.favoritesOnly {
		r.showFavorites()
	} else {
		r.list.Refresh()
		r.refreshRadioSelection()
	}
}
