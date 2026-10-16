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

const maxRadioRecent = 20

func (r *radioView) loadRadioPreferences(prefs fyne.Preferences) {
	r.countryCode = prefs.String(radioCountryPreference)
	r.favorites = r.loadRadioStations(prefs, radioFavoritesPreference)
	r.recent = r.loadRadioStations(prefs, radioRecentPreference)
}

func (r *radioView) loadRadioStations(prefs fyne.Preferences, key string) []radiobrowser.Station {
	data := prefs.String(key)
	if data == "" {
		return nil
	}
	var stations []radiobrowser.Station
	if err := json.Unmarshal([]byte(data), &stations); err != nil {
		r.screen.logRadioError("load "+key, err)
		return nil
	}
	return stations
}

func (r *radioView) saveRadioStations(key string, stations []radiobrowser.Station) error {
	data, err := json.Marshal(stations)
	if err != nil {
		return err
	}
	fyne.CurrentApp().Preferences().SetString(key, string(data))
	return nil
}

func (r *radioView) showFavorites() {
	r.cancelRadioSearch()
	r.mode = radioModeFavorites
	stations := append([]radiobrowser.Station(nil), r.favorites...)
	sort.Slice(stations, func(i, j int) bool { return strings.ToLower(stations[i].Name) < strings.ToLower(stations[j].Name) })
	r.setStations(stations)
	if len(stations) == 0 {
		r.status.SetText(lang.L("No favorites yet — select a station and tap Save favorite"))
		return
	}
	r.status.SetText(fmt.Sprintf(lang.L("Saved %d favorites"), len(stations)))
}

// showRecent lists stations cast before, most recent first.
func (r *radioView) showRecent() {
	r.cancelRadioSearch()
	r.mode = radioModeRecent
	r.setStations(append([]radiobrowser.Station(nil), r.recent...))
	if len(r.recent) == 0 {
		r.status.SetText(lang.L("No recently played stations"))
		return
	}
	r.status.SetText(fmt.Sprintf(lang.L("Recently played %d stations"), len(r.recent)))
}

func (r *radioView) recordRecent(station radiobrowser.Station) {
	recent := []radiobrowser.Station{station}
	for _, previous := range r.recent {
		if previous.UUID != station.UUID && len(recent) < maxRadioRecent {
			recent = append(recent, previous)
		}
	}
	r.recent = recent
	if err := r.saveRadioStations(radioRecentPreference, recent); err != nil {
		r.screen.logRadioError("save recent", err)
	}
}

func (r *radioView) showPopular() {
	r.name.SetText("")
	r.mode = radioModeSearch
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
	if err := r.saveRadioStations(radioFavoritesPreference, r.favorites); err != nil {
		r.status.SetText(err.Error())
		return
	}
	if r.mode == radioModeFavorites {
		r.showFavorites()
	} else {
		r.list.Refresh()
		r.refreshRadioSelection()
	}
}
