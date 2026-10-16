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

// radioStationRow takes taps from the list item. It is deliberately not
// DoubleTappable: drivers delay single taps on those to wait for a second.
type radioStationRow struct {
	widget.BaseWidget
	title, details *widget.Label
	icon           *canvas.Image
	id             widget.ListItemID
	onTap          func(widget.ListItemID)
}

func newRadioStationRow(onTap func(widget.ListItemID)) *radioStationRow {
	row := &radioStationRow{
		title:   widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		details: widget.NewLabel(""), icon: canvas.NewImageFromResource(theme.MediaMusicIcon()),
		id: -1, onTap: onTap,
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

func (r *radioStationRow) Tapped(*fyne.PointEvent) {
	if r.onTap != nil && r.id >= 0 {
		r.onTap(r.id)
	}
}

func (r *radioStationRow) setStation(id widget.ListItemID, station radiobrowser.Station, favorite, playing bool, asset *metadata.ArtworkAsset) {
	r.id = id
	name := station.Name
	if favorite {
		name = "★ " + name
	}
	if playing {
		name = "▶ " + name
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
	if tags := radioStationTags(station.Tags, maxRadioRowTags); tags != "" {
		parts = append(parts, tags)
	}
	return strings.Join(parts, " · ")
}

// Rows show the leading genre tags; the label truncates anything wider.
const maxRadioRowTags = 3

func radioStationTags(tags string, limit int) string {
	var result []string
	seen := make(map[string]bool)
	for tag := range strings.SplitSeq(tags, ",") {
		tag = strings.TrimSpace(tag)
		key := strings.ToLower(tag)
		if tag == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, tag)
		if len(result) == limit {
			break
		}
	}
	return strings.Join(result, ", ")
}
