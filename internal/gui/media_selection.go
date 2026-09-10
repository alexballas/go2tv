//go:build !(android || ios)

package gui

import (
	"image/color"
	"path/filepath"

	ttwidget "github.com/alexballas/fyne-tooltip/widget"
	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/driver/desktop"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/layout"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/mediamodel"
)

const (
	subtitleAutomatic = "Automatic"
	subtitleEmbedded  = "Embedded Track"
	subtitleExternal  = "External File"
	subtitleNone      = "None"
)

type mediaSelectionCard struct {
	screen      *FyneScreen
	source      *widget.Select
	subtitles   *ttwidget.Select
	media, subs *selectionRow
	content     *fyne.Container
	syncing     bool
}

// Both file selections share the same padding and stretch across the card.
type selectionRow struct {
	widget.BaseWidget
	name, path  *widget.TextSegment
	text        *selectionText
	actions     *fyne.Container
	reservePath bool
}

func newSelectionRow(actions ...fyne.CanvasObject) *selectionRow {
	r := &selectionRow{
		name:    &widget.TextSegment{Style: widget.RichTextStyle{TextStyle: fyne.TextStyle{Bold: true}}},
		path:    &widget.TextSegment{Style: widget.RichTextStyle{ColorName: theme.ColorNamePlaceHolder}},
		actions: container.NewHBox(actions...),
	}
	r.text = &selectionText{RichText: ttwidget.NewRichText(r.name, r.path)}
	r.text.ExtendBaseWidget(r.text)
	r.text.Truncation = fyne.TextTruncateEllipsis
	r.ExtendBaseWidget(r)
	return r
}

type selectionText struct {
	*ttwidget.RichText
	title *widget.TextSegment
}

func (t *selectionText) MouseIn(e *desktop.MouseEvent) {
	t.SetToolTip("")
	if t.title != nil {
		width := fyne.MeasureText(t.title.Text, theme.SizeForWidget(theme.SizeNameText, t), t.title.Style.TextStyle).Width
		available := t.Size().Width - 2*theme.SizeForWidget(theme.SizeNameInnerPadding, t)
		if width > available {
			t.SetToolTip(t.title.Text)
		}
	}
	t.RichText.MouseIn(e)
}

func (r *selectionRow) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(nil)
	bg.CornerRadius = 6
	body := container.NewBorder(nil, nil, nil, container.NewCenter(r.actions), r.text)
	renderer := &selectionRowRenderer{WidgetRenderer: widget.NewSimpleRenderer(container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(4, 4, 8, 8), body))), row: r, background: bg}
	renderer.Refresh()
	return renderer
}

func (r *selectionRow) setPath(path, empty string) {
	name := empty
	if path != "" {
		name = filepath.Base(path)
	}
	r.name.Text = name
	r.path.Text = path
	r.text.Segments = []widget.RichTextSegment{r.name}
	if path != "" || r.reservePath {
		r.text.Segments = append(r.text.Segments, r.path)
	}
	r.text.Refresh()
}

func newMediaSelectionCard(s *FyneScreen, preview, clearSubs *widget.Button) *mediaSelectionCard {
	c := &mediaSelectionCard{screen: s}
	s.mediaSelection = c
	s.MediaBrowse.SetText(lang.L("Browse…"))
	s.SubsBrowse.SetText(lang.L("Browse…"))
	preview.SetText(lang.L("Preview"))
	s.ClearMedia.SetText(lang.L("Clear"))
	clearSubs.SetText(lang.L("Clear"))
	for _, b := range []*widget.Button{preview, s.ClearMedia, clearSubs} {
		b.SetIcon(nil)
		b.Importance = widget.LowImportance
	}
	c.media = newSelectionRow(preview, s.MediaBrowse, s.ClearMedia)
	c.media.text.title = c.media.name
	// Main source height stays stable even before a file is selected.
	c.media.reservePath = true
	c.subs = newSelectionRow(s.SubsBrowse, clearSubs)
	c.source = widget.NewSelect([]string{lang.L("Local File"), lang.L("URL")}, func(value string) {
		if c.syncing {
			return
		}
		s.ExternalMediaURL.SetChecked(value == lang.L("URL"))
		c.refresh()
	})
	c.subtitles = ttwidget.NewSelect([]string{lang.L(subtitleAutomatic), lang.L(subtitleEmbedded), lang.L(subtitleExternal), lang.L(subtitleNone)}, func(value string) {
		if c.syncing {
			return
		}
		s.CustomSubsCheck.SetChecked(value != lang.L(subtitleAutomatic))
		clearsubsAction(s)
		if value == lang.L(subtitleAutomatic) && s.mediafile != "" && !s.ExternalMediaURL.Checked {
			autoSelectNextSubs(s.mediafile, s)
		}
		c.refresh()
	})
	// Keep explicit modes from picking up a sidecar when the queue advances.
	s.SelectInternalSubs.OnChanged = func(item string) {
		if item == "" {
			return
		}
		s.subsfile = ""
		s.SubsText.SetText("")
		s.CustomSubsCheck.SetChecked(true)
		c.refresh()
	}
	c.subtitles.Selected = lang.L(subtitleAutomatic)
	sourceLabel := widget.NewLabel(lang.L("Source"))
	subtitleLabel := widget.NewLabel(lang.L("Subtitles"))
	labelWidth := fyne.Max(sourceLabel.MinSize().Width, subtitleLabel.MinSize().Width)
	sourceRow := container.New(selectionFieldLayout{labelWidth: labelWidth}, sourceLabel, c.source)
	subtitleRow := container.New(selectionFieldLayout{labelWidth: labelWidth}, subtitleLabel, c.subtitles)
	selectors := container.NewGridWithColumns(2, sourceRow, subtitleRow)
	mediaArea := container.New(reservedSelectionLayout{}, c.media, s.MediaText)
	c.content = container.NewVBox(selectors, mediaArea, s.SelectInternalSubs, c.subs)
	c.bindSource(preview)
	c.refresh()
	return c
}

// Keep related dropdowns aligned even when translated labels differ in width.
type selectionFieldLayout struct {
	labelWidth float32
}

func (l selectionFieldLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	label, control := objects[0].MinSize(), objects[1].MinSize()
	return fyne.NewSize(l.labelWidth+theme.Padding()+control.Width, fyne.Max(label.Height, control.Height))
}

func (l selectionFieldLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	controlX := l.labelWidth + theme.Padding()
	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(fyne.NewSize(l.labelWidth, size.Height))
	objects[1].Move(fyne.NewPos(controlX, 0))
	objects[1].Resize(fyne.NewSize(fyne.Max(0, size.Width-controlX), size.Height))
}

// Source alternatives share one area sized for the largest state, including
// hidden controls. New sources belong in this same area to keep the card stable.
type reservedSelectionLayout struct{}

func (reservedSelectionLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := fyne.NewSize(0, 0)
	for _, object := range objects {
		size = size.Max(object.MinSize())
	}
	return size
}

func (reservedSelectionLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		if object.Visible() {
			object.Move(fyne.NewPos(0, 0))
			object.Resize(fyne.NewSize(size.Width, object.MinSize().Height))
		}
	}
}

func (c *mediaSelectionCard) refresh() {
	s := c.screen
	c.syncing = true
	defer func() { c.syncing = false }()
	urlMode := s.ExternalMediaURL.Checked
	source := lang.L("Local File")
	if urlMode {
		source = lang.L("URL")
	}
	if c.source.Selected != source {
		c.source.SetSelected(source)
	}
	c.media.setPath(s.mediafile, lang.L("No media selected"))
	if s.mediafile == "" {
		c.media.path.Text = lang.L("Choose a media file to start casting.")
		c.media.text.Refresh()
	}
	setSelectionVisible(c.media, !urlMode)
	setSelectionVisible(s.MediaText, urlMode)
	setSelectionVisible(s.MediaBrowse, !urlMode)
	if urlMode && !s.MediaText.Disabled() {
		s.MediaText.SetPlaceHolder(lang.L("Enter media URL…"))
	}
	if s.ExternalMediaURL.Disabled() {
		c.source.Disable()
	} else {
		c.source.Enable()
	}
	mediaKind := mediamodel.KindForPath(s.mediafile)
	subtitlesUnavailable := mediaKind == mediamodel.MediaKindAudio || mediaKind == mediamodel.MediaKindImage
	if subtitlesUnavailable {
		if c.subtitles.Selected != lang.L(subtitleAutomatic) {
			c.subtitles.SetSelected(lang.L(subtitleAutomatic))
		}
		s.CustomSubsCheck.SetChecked(false)
		s.SelectInternalSubs.ClearSelected()
		s.SubsText.SetText("")
		s.subsfile = ""
	}
	if s.ExternalMediaURL.Disabled() || subtitlesUnavailable {
		c.subtitles.Disable()
	} else {
		c.subtitles.Enable()
	}
	c.subtitles.SetToolTip("")
	switch mediaKind {
	case mediamodel.MediaKindAudio:
		c.subtitles.SetToolTip(lang.L("Not available for audio"))
	case mediamodel.MediaKindImage:
		c.subtitles.SetToolTip(lang.L("Not available for images"))
	}
	externalSubs := c.subtitles.Selected == lang.L(subtitleExternal)
	c.subs.setPath(s.subsfile, lang.L("No subtitle selected"))
	setSelectionVisible(c.subs, externalSubs)
	setSelectionVisible(s.SelectInternalSubs, c.subtitles.Selected == lang.L(subtitleEmbedded))
	if externalSubs && !c.subtitles.Disabled() {
		s.SubsBrowse.Enable()
	} else {
		s.SubsBrowse.Disable()
	}
	s.refreshPlaybackReadiness()
	if c.subtitles.Disabled() {
		s.SelectInternalSubs.Disable()
	} else {
		s.SelectInternalSubs.Enable()
	}
	c.content.Refresh()
}

// Keep source transitions shared with queue selection and live-stream controls.
func (c *mediaSelectionCard) bindSource(preview *widget.Button) {
	s := c.screen
	var mediafileOld, mediafileOldText string

	s.ExternalMediaURL.OnChanged = func(b bool) {
		defer c.refresh()
		if b {
			s.NextMediaCheck.SetChecked(false)
			s.NextMediaCheck.Disable()
			s.MediaBrowse.Disable()
			preview.Disable()
			s.SkipNextButton.Disable()
			s.SkipPreviousButton.Disable()

			// keep old values
			mediafileOld = s.mediafile
			mediafileOldText = s.MediaText.Text

			// Clear the Media Text Area
			clearCurrentMediaSelection(s)

			// Set some Media text defaults
			// to indicate that we're expecting a URL
			s.MediaText.SetPlaceHolder(lang.L("Enter media URL…"))
			s.MediaText.Enable()
			setPlayPauseView("", s)
			return
		}

		if !s.NextMediaCheck.Checked {
			s.LoopSelectedCheck.Enable()
		}

		if !s.LoopSelectedCheck.Checked {
			s.NextMediaCheck.Enable()
		}

		s.MediaBrowse.Enable()
		preview.Enable()
		s.MediaText.SetPlaceHolder("")
		s.MediaText.Disable()
		restoreMediaInputState(s, mediafileOld, mediafileOldText)
	}
}

func setSelectionVisible(object fyne.CanvasObject, visible bool) {
	if object.Visible() == visible {
		return
	}
	if visible {
		object.Show()
	} else {
		object.Hide()
	}
}

type selectionRowRenderer struct {
	fyne.WidgetRenderer
	row        *selectionRow
	background *canvas.Rectangle
}

func (r *selectionRowRenderer) Refresh() {
	background := color.NRGBAModel.Convert(r.row.Theme().Color(theme.ColorNameInputBackground, fyne.CurrentApp().Settings().ThemeVariant())).(color.NRGBA)
	// Blend into the card for a subtle selection surface in either theme.
	background.A = uint8(uint16(background.A) * 3 / 8)
	r.background.FillColor = background
	r.WidgetRenderer.Refresh()
}
