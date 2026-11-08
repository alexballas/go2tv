//go:build android || ios

package gui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/layout"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"

	"go2tv.app/go2tv/v2/internal/mediamodel"
	"go2tv.app/go2tv/v2/internal/mediasource"
	"go2tv.app/go2tv/v2/utils"
)

type mobileSubtitleMode int

const (
	mobileSubsAutomatic mobileSubtitleMode = iota
	mobileSubsEmbedded
	mobileSubsExternal
	mobileSubsNone
)

// Selection state belongs to the media, not the sheet's lifetime.
type mobileTracks struct {
	mode                               mobileSubtitleMode
	track                              int
	media                              string
	names                              []string
	cancel                             context.CancelFunc
	probing                            bool
	probeError                         error
	extracted                          string
	source                             *widget.Select
	browse                             *widget.Button
	summary                            *valueField
	modeSelect, embedded               *widget.Select
	modes                              []mobileSubtitleMode
	title                              *widget.Label
	relayout                           func()
	audioRow, embeddedRow, externalRow *fyne.Container
	subs                               *mobileSubtitleControls
	hint                               *widget.Label
	sheet                              *mobileTrackSheet
	syncing                            bool
}

func newMobileTracks(s *FyneScreen, browse, clear *widget.Button, subs *mobileSubtitleControls) *mobileTracks {
	c := &s.tracks
	c.browse, c.subs = browse, subs
	c.syncing = true
	c.summary = newTrackSummary(func() { c.show(s) })
	var audio *widget.Select
	audio = widget.NewSelect(nil, func(string) {
		s.chooseAudioTrack(audio.SelectedIndex())
		c.refreshSummary(s)
	})
	c.audioRow = container.NewVBox(widget.NewLabel(lang.L("Audio")), audio)
	c.audioRow.Hide()
	s.audioPicker = audioPicker{selectWidget: audio, objects: []fyne.CanvasObject{c.audioRow}, hint: func(string) {
		if s.mobilePlaybackBusy() {
			audio.Disable()
		}
	}}
	c.modeSelect = widget.NewSelect(nil, func(string) {
		index := c.modeSelect.SelectedIndex()
		if !c.syncing && index >= 0 && index < len(c.modes) {
			c.setMode(s, c.modes[index])
		}
	})
	c.embedded = widget.NewSelect(nil, func(string) {
		if c.syncing {
			return
		}
		c.track = c.embedded.SelectedIndex()
		c.clearExtracted(s)
		c.refreshSummary(s)
	})
	c.embedded.PlaceHolder = lang.L("No Embedded Subs")
	c.embeddedRow = container.NewVBox(widget.NewLabel(lang.L("From video")), c.embedded)
	manual := widget.NewButtonWithIcon(lang.L("Browse subtitle file…"), theme.FolderOpenIcon(), func() { subsAction(s) })
	subs.browse = manual
	c.externalRow = container.NewVBox(widget.NewLabel(lang.L("Subtitle file")), container.NewBorder(nil, nil, nil, clear, subs.field), manual)
	c.hint = widget.NewLabel("")
	c.hint.Wrapping = fyne.TextWrapWord
	c.hint.Importance = widget.MediumImportance
	c.hint.SizeName = theme.SizeNameCaptionText
	c.syncing = false
	return c
}

func (c *mobileTracks) setMode(s *FyneScreen, mode mobileSubtitleMode) {
	c.clearExtracted(s)
	c.mode = mode
	clearsubsAction(s)
	c.refresh(s)
}

func (c *mobileTracks) clearExtracted(s *FyneScreen) {
	if c.extracted == "" {
		return
	}
	if s.subsfile != nil && s.subsfile.Path() == c.extracted {
		s.subsfile = nil
		s.SubsText.SetText("")
	}
	removeTempFile(&c.extracted)
}

// No media means no settings. Unknown nonempty URLs can still use manual
// subtitles; known audio, images and radio cannot.
func (c *mobileTracks) available(s *FyneScreen) bool {
	input := strings.TrimSpace(s.MediaText.Text)
	if s.ExternalMediaURL.Checked {
		if input == "" {
			return false
		}
	} else if s.mediafile == nil {
		return false
	}
	kind := s.subtitleKindForInput(input)
	return kind != mediamodel.MediaKindAudio && kind != mediamodel.MediaKindImage
}

func (c *mobileTracks) refreshSummary(s *FyneScreen) {
	if c.summary == nil {
		return
	}
	wasVisible, oldSize := c.summary.Visible(), c.summary.MinSize()
	available := c.available(s)
	if available {
		c.summary.Show()
	} else {
		c.summary.Hide()
	}
	title := lang.L("Subtitles")
	lines := []string{}
	if c.audioRow.Visible() && len(s.audioPicker.selectWidget.Options) > 1 {
		title = lang.L("Audio & subtitles")
		lines = append(lines, s.audioPicker.selectWidget.Selected)
	}
	subtitle := lang.L("Automatic")
	switch c.mode {
	case mobileSubsEmbedded:
		subtitle = lang.L("From video")
		if c.track >= 0 && c.track < len(c.names) {
			subtitle += " · " + c.names[c.track]
		}
	case mobileSubsExternal:
		subtitle = lang.L("Choose subtitle file…")
		if s.subsfile != nil {
			subtitle = s.subsfile.Name()
		}
	case mobileSubsNone:
		subtitle = lang.L("Off")
	}
	c.summary.heading.SetText(title)
	lines = append(lines, subtitle)
	c.summary.Set(strings.Join(lines, "\n"), "")
	if c.title != nil {
		c.title.SetText(title)
	}
	if !available && c.sheet != nil {
		c.sheet.close()
	}
	// A hidden child updates the source VBox, but the main layout also needs
	// refreshing to return its previously allocated height to the device list.
	if c.relayout != nil && (wasVisible != available || oldSize != c.summary.MinSize()) {
		c.relayout()
	}
}

func (c *mobileTracks) refreshModes() {
	c.modes = []mobileSubtitleMode{mobileSubsAutomatic}
	options := []string{lang.L("Automatic")}
	if len(c.names) > 0 {
		c.modes = append(c.modes, mobileSubsEmbedded)
		options = append(options, lang.L("From video"))
	}
	c.modes = append(c.modes, mobileSubsExternal, mobileSubsNone)
	options = append(options, lang.L("Choose subtitle file…"), lang.L("Off"))
	if c.mode == mobileSubsEmbedded && len(c.names) == 0 {
		c.mode = mobileSubsAutomatic
	}
	c.modeSelect.SetOptions(options)
	for i, mode := range c.modes {
		if mode == c.mode {
			c.modeSelect.SetSelectedIndex(i)
			break
		}
	}
}

func (c *mobileTracks) refresh(s *FyneScreen) {
	if c.summary == nil {
		return
	}
	c.syncing = true
	defer func() { c.syncing = false }()
	c.syncSource(s)
	c.subs.refresh(s, s.MediaText.Text)
	unavailable := !c.available(s)
	busy := s.mobilePlaybackBusy() || s.getScreenState() == "Playing" || s.getScreenState() == "Paused"
	if unavailable || busy {
		c.modeSelect.Disable()
	} else {
		c.modeSelect.Enable()
	}
	c.probe(s, !unavailable)
	c.refreshModes()
	c.embeddedRow.Hidden = c.mode != mobileSubsEmbedded
	c.externalRow.Hidden = c.mode != mobileSubsExternal
	c.embedded.SetOptions(c.names)
	if len(c.names) > 0 {
		c.embedded.SetSelectedIndex(c.track)
	} else {
		c.embedded.ClearSelected()
	}
	if busy || len(c.names) == 0 {
		c.embedded.Disable()
	} else {
		c.embedded.Enable()
	}
	if busy {
		s.audioPicker.selectWidget.Disable()
		c.subs.browse.Disable()
		c.subs.clear.Disable()
		c.subs.field.OnTapped = nil
	}
	hint := lang.L("Selections apply when casting starts.")
	switch {
	case busy:
		hint = lang.L("Stop playback to change audio or subtitles.")
	case unavailable:
		hint = c.subs.placeholder
	case c.mode == mobileSubsEmbedded && c.probing:
		hint = lang.L("Searching for subtitle tracks…")
	case c.mode == mobileSubsEmbedded && c.probeError != nil && !errors.Is(c.probeError, utils.ErrNoSubs):
		hint = lang.L("Unable to read embedded tracks. Choose an external subtitle file.")
	case c.mode == mobileSubsEmbedded && len(c.names) == 0:
		hint = lang.L("No Embedded Subs")
	case c.mode == mobileSubsExternal:
		hint = lang.L("Use a subtitle file from your device.")
	case c.mode == mobileSubsAutomatic:
		hint = lang.L("Use matching subtitles when available.")
	}
	c.hint.SetText(hint)
	c.refreshSummary(s)
	if c.sheet != nil {
		c.sheet.Refresh()
	}
}

func (c *mobileTracks) probe(s *FyneScreen, available bool) {
	media := ""
	uri := s.mediafile
	if available && uri != nil && s.ffmpegPath != "" && !s.ExternalMediaURL.Checked {
		media = uri.String() + "|" + s.ffmpegPath
	}
	if media == c.media {
		return
	}
	if c.cancel != nil {
		c.cancel()
	}
	c.media, c.names, c.track, c.probing, c.probeError = media, nil, 0, false, nil
	if media == "" {
		return
	}
	ctx, cancel, done, ok := s.workers.begin(s.torrent.ctx)
	if !ok {
		return
	}
	c.cancel, c.probing = cancel, true
	ffmpeg := s.ffmpegPath
	go func() {
		defer done()
		reader, err := mobileSubtitleReader(ctx, uri)
		var names []string
		if err == nil {
			names, err = utils.GetSubsReaderContext(ctx, ffmpeg, reader)
			_ = reader.Close()
		}
		fyne.Do(func() {
			defer cancel()
			if ctx.Err() != nil || media != c.media {
				return
			}
			c.names, c.probeError, c.probing = names, err, false
			c.refresh(s)
		})
	}()
}

func mobileSubtitleReader(ctx context.Context, uri fyne.URI) (io.ReadSeekCloser, error) {
	if source, ok := mediasource.Lookup(uri.Path()); ok {
		return source.Open(ctx)
	}
	return storage.ReaderSeeker(uri)
}

func (c *mobileTracks) syncSource(s *FyneScreen) {
	if c.source == nil {
		return
	}
	wasVisible, oldSize := c.browse.Visible(), c.browse.MinSize()
	defer func() {
		if c.relayout != nil && (wasVisible != c.browse.Visible() || oldSize != c.browse.MinSize()) {
			c.relayout()
		}
	}()
	value := lang.L("Local File")
	switch {
	case s.ExternalMediaURL.Checked:
		value = lang.L("URL")
		if _, radio := s.radioStationForURL(s.MediaText.Text); radio {
			value = lang.L("Radio")
		}
	case torrentMediaSelected(s):
		value = lang.L("Torrent")
	}
	c.source.SetSelected(value)
	c.browse.SetText(lang.L("Browse"))
	c.browse.Enable()
	switch value {
	case lang.L("URL"):
		c.browse.Hide()
	case lang.L("Torrent"), lang.L("Radio"):
		c.browse.SetText(lang.L("Change…"))
		c.browse.Show()
	default:
		c.browse.Show()
	}
}

func (c *mobileTracks) show(s *FyneScreen) {
	if c.sheet != nil || !c.available(s) {
		return
	}
	c.refresh(s)
	sheet := &mobileTrackSheet{canvas: s.Current.Canvas()}
	c.sheet = sheet
	close := func() {
		if c.sheet != sheet {
			return
		}
		sheet.canvas.Overlays().Remove(sheet)
		c.sheet = nil
		c.refreshSummary(s)
	}
	sheet.close = close
	c.title = widget.NewLabelWithStyle(lang.L("Subtitles"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	c.refreshSummary(s)
	header := container.NewBorder(nil, nil, nil, widget.NewButtonWithIcon("", theme.CancelIcon(), close), c.title)
	fields := container.NewVBox(c.audioRow, widget.NewLabel(lang.L("Subtitles")), c.modeSelect, c.embeddedRow, c.externalRow, c.hint)
	done := widget.NewButton(lang.L("Done"), close)
	done.Importance = widget.HighImportance
	// Keep Done clear of the rounded sheet and physical screen corners.
	sheet.content = container.New(layout.NewCustomPaddedLayout(theme.InnerPadding(), 24, 16, 16),
		container.NewBorder(header, done, nil, nil, container.NewVScroll(fields)))
	sheet.ExtendBaseWidget(sheet)
	pos, size := sheet.canvas.InteractiveArea()
	sheet.Move(pos)
	sheet.Resize(size)
	sheet.canvas.Overlays().Add(sheet)
}

// Full-canvas overlay keeps the sheet bottom-aligned after rotation. Only its
// fields scroll on short screens; header and Done remain reachable.
type mobileTrackSheet struct {
	widget.BaseWidget
	canvas  fyne.Canvas
	content fyne.CanvasObject
	close   func()
}

func (s *mobileTrackSheet) Tapped(e *fyne.PointEvent) {
	if e.Position.Y < s.content.Position().Y || e.Position.X < s.content.Position().X || e.Position.X > s.content.Position().X+s.content.Size().Width {
		s.close()
	}
}

func (s *mobileTrackSheet) Hide() { s.close() }

func (s *mobileTrackSheet) CreateRenderer() fyne.WidgetRenderer {
	r := &mobileTrackSheetRenderer{sheet: s, shade: canvas.NewRectangle(nil), panel: canvas.NewRectangle(nil)}
	r.Refresh()
	return r
}

type mobileTrackSheetRenderer struct {
	sheet        *mobileTrackSheet
	shade, panel *canvas.Rectangle
}

func (r *mobileTrackSheetRenderer) Layout(size fyne.Size) {
	r.shade.Resize(size)
	width, height := min(size.Width, 600), min(size.Height, 560)
	pos := fyne.NewPos((size.Width-width)/2, size.Height-height)
	r.panel.Move(pos)
	r.panel.Resize(fyne.NewSize(width, height))
	r.sheet.content.Move(pos)
	r.sheet.content.Resize(fyne.NewSize(width, height))
}
func (r *mobileTrackSheetRenderer) MinSize() fyne.Size { return fyne.NewSize(0, 0) }
func (r *mobileTrackSheetRenderer) Refresh() {
	th, variant := r.sheet.Theme(), fyne.CurrentApp().Settings().ThemeVariant()
	r.shade.FillColor = th.Color(theme.ColorNameShadow, variant)
	r.panel.FillColor = th.Color(theme.ColorNameBackground, variant)
	r.panel.CornerRadius = 20
	r.shade.Refresh()
	r.panel.Refresh()
	r.sheet.content.Refresh()
}
func (r *mobileTrackSheetRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.shade, r.panel, r.sheet.content}
}
func (*mobileTrackSheetRenderer) Destroy() {}

// Snapshot the user's choice before background playback work; modes are
// explicit so None/External never fall back to automatic torrent captions.
func prepareMobileSubtitles(s *FyneScreen, ctx context.Context, automaticEmbedded bool) error {
	var mode mobileSubtitleMode
	var track int
	var uri fyne.URI
	var external, hasExternal bool
	fyne.DoAndWait(func() {
		mode, track, uri = s.tracks.mode, s.tracks.track, s.mediafile
		external = s.ExternalMediaURL.Checked
		s.tracks.clearExtracted(s)
		hasExternal = s.subsfile != nil
	})
	if external || uri == nil || mediamodel.KindForPath(uri.Name()) != mediamodel.MediaKindVideo {
		return nil
	}
	if mode == mobileSubsAutomatic && s.ffmpegPath == "" {
		return nil
	}
	if mode != mobileSubsEmbedded && !(mode == mobileSubsAutomatic && !hasExternal && automaticEmbedded && !torrentMediaSelected(s)) {
		return nil
	}
	reader, err := mobileSubtitleReader(ctx, uri)
	if err != nil {
		if mode == mobileSubsAutomatic {
			return ctx.Err()
		}
		return fmt.Errorf("prepareMobileSubtitles: %w", err)
	}
	defer reader.Close()
	if mode == mobileSubsAutomatic {
		names, err := utils.GetSubsReaderContext(ctx, s.ffmpegPath, reader)
		if err != nil || len(names) == 0 {
			return ctx.Err()
		}
	}
	out, err := createMobileCacheTemp("go2tv-embedded-*.srt")
	if err != nil {
		return fmt.Errorf("prepareMobileSubtitles: %w", err)
	}
	path := out.Name()
	defer func() { _ = out.Close() }()
	err = utils.ExtractSubReaderContext(ctx, s.ffmpegPath, track, reader, out)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil || ctx.Err() != nil {
		_ = os.Remove(path)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if mode == mobileSubsAutomatic {
			return nil
		}
		return fmt.Errorf("prepareMobileSubtitles: %w. %s", err, lang.L("Choose an external text subtitle file for this track."))
	}
	accepted := false
	fyne.DoAndWait(func() {
		if ctx.Err() != nil || s.mediafile == nil || s.mediafile.String() != uri.String() || s.tracks.mode != mode || s.tracks.track != track {
			return
		}
		accepted = true
		s.tracks.extracted = path
		s.subsfile = storage.NewFileURI(path)
		s.SubsText.SetText(filepath.Base(path))
	})
	if !accepted {
		_ = os.Remove(path)
		return context.Canceled
	}
	return nil
}

func mobileAutomaticSubtitles(s *FyneScreen) bool { return s.tracks.mode == mobileSubsAutomatic }
