package gui

import (
	"math"
	"sort"
	"strings"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
)

type radioCountryPicker struct {
	search  *widget.Entry
	list    *widget.List
	popup   dialog.Dialog
	choices []radiobrowser.Country
}

func (r *radioView) setRadioCountries(countries []radiobrowser.Country) {
	tag := radioDisplayLanguage()
	namer := display.Regions(tag)
	localized := make([]radiobrowser.Country, len(countries))
	for i, country := range countries {
		localized[i] = country
		if name := radioRegionName(namer, country.Code); name != "" {
			localized[i].Name = name
		}
	}
	sort.SliceStable(localized, func(i, j int) bool { return strings.ToLower(localized[i].Name) < strings.ToLower(localized[j].Name) })
	countries = localized
	r.countries = countries
	for _, country := range countries {
		if country.Code == r.countryCode {
			if country.Code == "" {
				r.country.SetText(lang.L("Select country"))
			} else {
				r.country.SetText(country.Name)
			}
			break
		}
	}
	if r.countryPicker != nil {
		r.countryPicker.refreshRadioCountryChoices(countries)
	}
}

func (p *radioCountryPicker) refreshRadioCountryChoices(countries []radiobrowser.Country) {
	query := strings.ToLower(strings.TrimSpace(p.search.Text))
	p.choices = []radiobrowser.Country{{Name: lang.L("All countries (clear selection)")}}
	for _, country := range countries {
		if strings.Contains(strings.ToLower(country.Name), query) || strings.EqualFold(country.Code, query) {
			p.choices = append(p.choices, country)
		}
	}
	p.list.UnselectAll()
	p.list.Refresh()
}

func (r *radioView) chooseCountry() {
	if r.countryPicker != nil {
		r.countryPicker.popup.Show()
		return
	}
	if len(r.countries) == 0 {
		r.loadRadioCountries()
	}
	picker := &radioCountryPicker{search: widget.NewEntry()}
	picker.search.SetPlaceHolder(lang.L("Search countries"))
	picker.list = widget.NewList(func() int { return len(picker.choices) }, func() fyne.CanvasObject { return widget.NewLabel("") }, func(id widget.ListItemID, object fyne.CanvasObject) {
		object.(*widget.Label).SetText(picker.choices[id].Name)
	})
	picker.search.OnChanged = func(string) { picker.refreshRadioCountryChoices(r.countries) }
	picker.list.OnSelected = func(id widget.ListItemID) {
		country := picker.choices[id]
		r.countryCode = country.Code
		if country.Code == "" {
			r.country.SetText(lang.L("Select country"))
		} else {
			r.country.SetText(country.Name)
		}
		fyne.CurrentApp().Preferences().SetString(radioCountryPreference, country.Code)
		picker.popup.Hide()
		r.mode = radioModeSearch
		r.searchRadioStations()
	}
	picker.popup = dialog.NewCustom(lang.L("Country"), lang.L("Close"), container.NewBorder(picker.search, nil, nil, nil, picker.list), r.screen.Current)
	picker.popup.SetOnClosed(func() {
		if r.countryPicker == picker {
			r.countryPicker = nil
		}
	})
	r.countryPicker = picker
	picker.refreshRadioCountryChoices(r.countries)
	size := r.screen.Current.Canvas().Size()
	width, height := float32(330), float32(390)
	if size.Width >= 280 {
		width = min(width, size.Width-24)
	}
	if size.Height >= 360 {
		height = min(height, size.Height-32)
	}
	picker.popup.Resize(fyne.NewSize(width, height))
	picker.popup.Show()
}

// The country name truncates on narrow screens without resizing the search box.
type radioCountryButton struct{ widget.Button }

func newRadioCountryButton(text string, tapped func()) *radioCountryButton {
	button := &radioCountryButton{Button: widget.Button{Text: text, OnTapped: tapped}}
	button.ExtendBaseWidget(button)
	return button
}

type radioCountryRenderer struct {
	fyne.WidgetRenderer
	button *radioCountryButton
	label  *widget.RichText
}

func (b *radioCountryButton) CreateRenderer() fyne.WidgetRenderer {
	native := b.Button.CreateRenderer()
	renderer := &radioCountryRenderer{WidgetRenderer: native, button: b}
	for _, object := range native.Objects() {
		if text, ok := object.(*widget.RichText); ok {
			text.Truncation = fyne.TextTruncateEllipsis
			renderer.label = text
		}
	}
	return renderer
}

func (r *radioCountryRenderer) Layout(size fyne.Size) {
	r.WidgetRenderer.Layout(size)
	if r.label == nil {
		return
	}
	// A native button sizes its label to MinSize, which is one character plus
	// an ellipsis when truncation is enabled. Use the allocated width instead.
	padding := theme.SizeForWidget(theme.SizeNameInnerPadding, r.button)
	labelSize := fyne.NewSize(max(0, size.Width-padding*2), r.label.MinSize().Height)
	r.label.Resize(labelSize)
	r.label.Move(fyne.NewPos(padding, (size.Height-labelSize.Height)/2))
}

func (r *radioCountryRenderer) Refresh() {
	r.WidgetRenderer.Refresh()
	r.Layout(r.button.Size())
}

func (r *radioCountryRenderer) MinSize() fyne.Size {
	textSize := theme.SizeForWidget(theme.SizeNameText, r.button)
	padding := theme.SizeForWidget(theme.SizeNameInnerPadding, r.button)
	textWidth := fyne.MeasureText(lang.L("Country"), textSize, fyne.TextStyle{Bold: true}).Width
	width := float32(math.Ceil(float64(textWidth))) + padding*2
	return fyne.NewSize(width, r.WidgetRenderer.MinSize().Height)
}

func radioCountryName(code string) string {
	if name := radioRegionName(display.Regions(radioDisplayLanguage()), code); name != "" {
		return name
	}
	return code
}

func radioRegionName(namer display.Namer, code string) string {
	region, err := language.ParseRegion(code)
	if err != nil || namer == nil {
		return ""
	}
	return namer.Name(region)
}

// radioDisplayLanguage follows the UI translation in use, so country names
// never switch to a language the rest of the app is not shown in.
func radioDisplayLanguage() language.Tag {
	requested := language.Make(lang.SystemLocale().String())
	switch fyne.CurrentApp().Preferences().String("Language") {
	case "English":
		requested = language.English
	case "中文(简体)":
		requested = language.SimplifiedChinese
	}
	supported := []language.Tag{language.English, language.SimplifiedChinese, language.TraditionalChinese}
	_, index, _ := language.NewMatcher(supported).Match(requested)
	return supported[index]
}
