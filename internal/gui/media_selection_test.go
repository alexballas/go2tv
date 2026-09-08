//go:build !(android || ios)

package gui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"
)

func newMediaCardTestScreen(t *testing.T) (*FyneScreen, *mediaSelectionCard) {
	t.Helper()
	app := test.NewApp()
	t.Cleanup(app.Quit)
	s := newQueueMediaSelectionTestScreen()
	s.ExternalMediaURL = widget.NewCheck("", nil)
	s.MediaBrowse = widget.NewButton("", nil)
	s.SubsBrowse = widget.NewButton("", nil)
	s.ClearMedia = widget.NewButton("", func() { clearmediaAction(s) })
	s.NextMediaCheck = widget.NewCheck("", nil)
	s.LoopSelectedCheck = widget.NewCheck("", nil)
	s.SkipNextButton = widget.NewButton("", nil)
	s.SkipPreviousButton = widget.NewButton("", nil)
	c := newMediaSelectionCard(s, widget.NewButton("", nil), widget.NewButton("", func() { clearsubsAction(s) }))
	return s, c
}

func TestMediaCardSourceSwitchAndClear(t *testing.T) {
	s, c := newMediaCardTestScreen(t)
	if !c.media.Visible() || s.MediaText.Visible() || !s.MediaBrowse.Visible() {
		t.Fatal("local empty state should show its selection row and Browse")
	}
	path := filepath.Join(t.TempDir(), "movie.mp4")
	if err := setCurrentMediaPath(s, path); err != nil {
		t.Fatal(err)
	}
	c.source.SetSelected(lang.L("URL"))
	if !s.ExternalMediaURL.Checked || !s.MediaText.Visible() || s.MediaText.Disabled() || c.media.Visible() || s.MediaBrowse.Visible() {
		t.Fatal("URL mode should replace the local selection with an editable URL")
	}
	s.MediaText.SetText("https://example.org/movie.mp4")
	c.source.SetSelected(lang.L("Local File"))
	if s.mediafile != path || !c.media.Visible() || c.media.path.Text != path {
		t.Fatal("switching back should restore the local selection")
	}
	test.Tap(s.ClearMedia)
	if s.mediafile != "" || !c.media.Visible() || !s.MediaBrowse.Visible() {
		t.Fatal("Clear should restore the empty state")
	}
}

func TestMediaCardReservesHeightAcrossSources(t *testing.T) {
	s, c := newMediaCardTestScreen(t)
	media := newSectionCard("Media", c.content)
	playback := newSectionCard("Playback", widget.NewLabel("Controls"))
	column := container.NewVBox(media, playback)
	for _, width := range []float32{400, 640, 900} {
		for _, mode := range []string{subtitleAutomatic, subtitleExternal, subtitleEmbedded, subtitleNone} {
			c.subtitles.SetSelected(lang.L(mode))
			column.Resize(fyne.NewSize(width, column.MinSize().Height))
			column.Refresh()
			height, playbackY := media.Size().Height, playback.Position().Y
			tt := []struct {
				name, source, file string
			}{
				{"local selected", "Local File", "/media/movie.mp4"},
				{"URL", "URL", ""},
				{"local empty", "Local File", ""},
			}
			for _, tc := range tt {
				t.Run(mode+"/"+tc.name, func(t *testing.T) {
					c.source.SetSelected(lang.L(tc.source))
					s.mediafile = tc.file
					c.refresh()
					column.Refresh()
					if media.Size().Height != height || playback.Position().Y != playbackY {
						t.Fatalf("width %v: source change moved cards: media height %v (want %v), playback Y %v (want %v)", width, media.Size().Height, height, playback.Position().Y, playbackY)
					}
					area := c.content.Objects[1].(*fyne.Container)
					for _, control := range area.Objects {
						if control.Visible() && control.Position().Y+control.Size().Height > area.Size().Height {
							t.Fatal("visible control exceeds reserved media area")
						}
					}
				})
			}
		}
	}
	c.subtitles.SetSelected(lang.L(subtitleAutomatic))
	automaticHeight := c.content.MinSize().Height
	for _, mode := range []string{subtitleExternal, subtitleEmbedded} {
		c.subtitles.SetSelected(lang.L(mode))
		if c.content.MinSize().Height <= automaticHeight {
			t.Fatalf("%s controls should expand beyond Automatic height", mode)
		}
	}
	c.subtitles.SetSelected(lang.L(subtitleNone))
	if c.content.MinSize().Height != automaticHeight {
		t.Fatal("Automatic and None should leave no reserved subtitle space")
	}
}

func TestMediaCardSubtitleModes(t *testing.T) {
	s, c := newMediaCardTestScreen(t)
	dir := t.TempDir()
	movie := filepath.Join(dir, "movie.mp4")
	sidecar := filepath.Join(dir, "movie.srt")
	if err := os.WriteFile(sidecar, []byte("1\n00:00:00,000 --> 00:00:01,000\nHello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := setCurrentMediaPath(s, movie); err != nil {
		t.Fatal(err)
	}
	if s.subsfile != sidecar {
		t.Fatal("Automatic should discover a matching subtitle")
	}
	tt := []struct {
		mode               string
		external, embedded bool
	}{
		{subtitleNone, false, false},
		{subtitleEmbedded, false, true},
		{subtitleExternal, true, false},
	}
	for _, tc := range tt {
		t.Run(tc.mode, func(t *testing.T) {
			c.subtitles.SetSelected(lang.L(tc.mode))
			if s.subsfile != "" || s.SelectInternalSubs.Selected != "" {
				t.Fatal("mode switch must clear the previous subtitle")
			}
			if c.subs.Visible() != tc.external || s.SelectInternalSubs.Visible() != tc.embedded {
				t.Fatal("only the selected mode's control should be visible")
			}
			if err := setCurrentMediaPath(s, movie); err != nil {
				t.Fatal(err)
			}
			if s.subsfile != "" {
				t.Fatal("explicit subtitle mode must suppress automatic discovery")
			}
		})
	}
	// An embedded selection used to disable the external picker.
	c.subtitles.SetSelected(lang.L(subtitleEmbedded))
	s.SelectInternalSubs.Options = []string{"English"}
	s.SelectInternalSubs.SetSelected("English")
	if s.subsfile != "" || s.SelectInternalSubs.Selected != "English" {
		t.Fatal("embedded selection must replace any external subtitle")
	}
	c.subtitles.SetSelected(lang.L(subtitleExternal))
	if s.SubsBrowse.Disabled() {
		t.Fatal("External File must enable Browse after Embedded Track")
	}
	selectSubsFile(s, storage.NewFileURI(sidecar))
	if s.subsfile != sidecar || c.subs.path.Text != sidecar {
		t.Fatal("external selection should keep the subtitle path")
	}
	clearsubsAction(s)
	if s.subsfile != "" || !c.subs.Visible() {
		t.Fatal("Clear should leave the external picker available")
	}
	c.subtitles.SetSelected(lang.L(subtitleAutomatic))
	if s.subsfile != sidecar || s.CustomSubsCheck.Checked {
		t.Fatal("Automatic should restore discovery")
	}
	c.subtitles.SetSelected(lang.L(subtitleNone))
	selectSubsFile(s, storage.NewFileURI(sidecar))
	if c.subtitles.Selected != lang.L(subtitleExternal) || s.subsfile != sidecar {
		t.Fatal("dropping subtitles must switch to External File and retain selection")
	}
}

func TestMediaCardFileRowsAlign(t *testing.T) {
	s, c := newMediaCardTestScreen(t)
	s.mediafile = "/media/a-long-movie-filename.mp4"
	c.subtitles.SetSelected(lang.L(subtitleExternal))
	s.subsfile = "/media/subtitles/a-long-subtitle-filename.srt"
	c.refresh()
	for _, width := range []float32{400, 640, 900} {
		c.content.Resize(fyne.NewSize(width, c.content.MinSize().Height))
		if c.media.Position().X != c.subs.Position().X || c.media.Size().Width != c.subs.Size().Width || c.media.Size().Width != width {
			t.Fatalf("file rows must share full-width bounds at width %v: media %v/%v subs %v/%v", width, c.media.Position(), c.media.Size(), c.subs.Position(), c.subs.Size())
		}
		if c.subs.Position().Y+c.subs.Size().Height > c.content.Size().Height {
			t.Fatal("subtitle row must fit within the card")
		}
		if c.media.text.Position().X != c.subs.text.Position().X {
			t.Fatal("file text padding must match")
		}
	}
}

func TestMediaCardURLKeepsFocusDuringRefresh(t *testing.T) {
	s, c := newMediaCardTestScreen(t)
	w := fyne.CurrentApp().NewWindow("Media")
	defer w.Close()
	w.SetContent(c.content)
	c.source.SetSelected(lang.L("URL"))
	w.Canvas().Focus(s.MediaText)
	s.MediaText.OnChanged = func(string) { c.refresh() }
	test.Type(s.MediaText, "https://example.org/video.mp4")
	if w.Canvas().Focused() != s.MediaText || s.MediaText.Text != "https://example.org/video.mp4" {
		t.Fatal("URL edits must retain input focus and content")
	}
	s.ExternalMediaURL.Disable()
	c.refresh()
	if !c.source.Disabled() || !c.subtitles.Disabled() {
		t.Fatal("live-stream input lock must apply to both selectors")
	}
	s.ExternalMediaURL.Enable()
	c.refresh()
	if c.source.Disabled() || c.subtitles.Disabled() {
		t.Fatal("selectors must unlock after live streaming")
	}
}
