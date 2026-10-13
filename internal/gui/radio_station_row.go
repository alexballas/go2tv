package gui

import (
	"fmt"
	"strings"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/radiobrowser"
	"go2tv.app/go2tv/v2/metadata"
)

type radioStationRow struct {
	widget.BaseWidget
	title, details *widget.Label
	icon           *canvas.Image
}

func newRadioStationRow() *radioStationRow {
	row := &radioStationRow{
		title:   widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		details: widget.NewLabel(""), icon: canvas.NewImageFromResource(theme.MediaMusicIcon()),
	}
	row.title.Truncation = fyne.TextTruncateEllipsis
	row.details.Truncation = fyne.TextTruncateEllipsis
	row.icon.FillMode = canvas.ImageFillContain
	row.icon.SetMinSize(fyne.NewSize(40, 40))
	row.ExtendBaseWidget(row)
	return row
}

func (r *radioStationRow) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewBorder(nil, nil, r.icon, nil, container.NewVBox(r.title, r.details)))
}

func (r *radioStationRow) setStation(station radiobrowser.Station, favorite bool, asset *metadata.ArtworkAsset) {
	name := station.Name
	if favorite {
		name = "★ " + name
	}
	r.title.SetText(name)
	r.details.SetText(radioStationSummary(station))
	r.icon.Resource = theme.MediaMusicIcon()
	if asset != nil {
		r.icon.Resource = fyne.NewStaticResource(asset.ID+asset.Extension, asset.Data)
	}
	r.icon.Refresh()
}

func radioStationSummary(station radiobrowser.Station) string {
	var parts []string
	for _, value := range []string{station.Country, station.Language, station.Codec} {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	if station.Bitrate > 0 {
		parts = append(parts, fmt.Sprintf("%d kbps", station.Bitrate))
	}
	return strings.Join(parts, " · ")
}
