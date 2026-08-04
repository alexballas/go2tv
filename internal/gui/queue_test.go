//go:build !(android || ios)

package gui

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/test"
	"github.com/alexballas/refyne/v2/widget"
)

func newTraversalTestScreen(t *testing.T, currentPath string) *FyneScreen {
	t.Helper()

	return &FyneScreen{
		mediafile:    currentPath,
		mediaFormats: []string{".mp4", ".mp3", ".jpg"},
		videoFormats: []string{".mp4"},
		audioFormats: []string{".mp3"},
		imageFormats: []string{".jpg"},
		State:        "Stopped",
	}
}

func newQueueMediaSelectionTestScreen() *FyneScreen {
	return &FyneScreen{
		mediaFormats:       []string{".mp4"},
		videoFormats:       []string{".mp4"},
		MediaText:          widget.NewEntry(),
		SubsText:           widget.NewEntry(),
		SelectInternalSubs: widget.NewSelect(nil, nil),
		CustomSubsCheck:    widget.NewCheck("", nil),
		PlayPause:          widget.NewButton("", nil),
	}
}

func testQueueItems(paths ...string) []QueueItem {
	items := make([]QueueItem, 0, len(paths))
	for _, path := range paths {
		item, ok := (&FyneScreen{}).newQueueItem(path)
		if !ok {
			panic("invalid test media path: " + path)
		}
		items = append(items, item)
	}
	return items
}

func assertQueuePaths(t *testing.T, screen *FyneScreen, want []string) {
	t.Helper()

	if screen.SessionQueue == nil {
		t.Fatalf("expected queue")
	}
	if screen.SessionQueue.Len() != len(want) {
		t.Fatalf("expected %d queue items, got %d", len(want), screen.SessionQueue.Len())
	}
	items := screen.SessionQueue.Items()
	for i, wantPath := range want {
		if got := items[i].Path(); got != wantPath {
			t.Fatalf("queue item %d: got %q want %q", i, got, wantPath)
		}
	}
}

func TestGetAdjacentQueuedMediaSameTypeOnly(t *testing.T) {
	dir := t.TempDir()
	videoOne := filepath.Join(dir, "01.mp4")
	audioOne := filepath.Join(dir, "02.mp3")
	videoTwo := filepath.Join(dir, "03.mp4")

	screen := newTraversalTestScreen(t, videoOne)
	screen.SkinNextOnlySameTypes = true
	screen.SessionQueue = newSessionQueue(testQueueItems(videoOne, audioOne, videoTwo), 0)

	name, path, err := getAdjacentMedia(screen, 1)
	if err != nil {
		t.Fatalf("getAdjacentMedia failed: %v", err)
	}
	if name != filepath.Base(videoTwo) {
		t.Fatalf("unexpected next name: got %q want %q", name, filepath.Base(videoTwo))
	}
	if path != videoTwo {
		t.Fatalf("unexpected next path: got %q want %q", path, videoTwo)
	}
}

func TestGetAdjacentQueuedMediaStopsAtEnd(t *testing.T) {
	dir := t.TempDir()
	videoOne := filepath.Join(dir, "01.mp4")
	videoTwo := filepath.Join(dir, "02.mp4")

	screen := newTraversalTestScreen(t, videoTwo)
	screen.SessionQueue = newSessionQueue(testQueueItems(videoOne, videoTwo), 1)

	_, _, err := getAdjacentMedia(screen, 1)
	if !errors.Is(err, errNoNextQueueMedia) {
		t.Fatalf("expected errNoNextQueueMedia, got %v", err)
	}
}

func TestGetNextAutoPlayMediaWrapsAtEnd(t *testing.T) {
	dir := t.TempDir()
	videoOne := filepath.Join(dir, "01.mp4")
	videoTwo := filepath.Join(dir, "02.mp4")

	screen := newTraversalTestScreen(t, videoTwo)
	screen.SessionQueue = newSessionQueue(testQueueItems(videoOne, videoTwo), 1)

	name, path, err := getNextAutoPlayMediaOrError(screen)
	if err != nil {
		t.Fatalf("getNextAutoPlayMediaOrError failed: %v", err)
	}
	if name != filepath.Base(videoOne) {
		t.Fatalf("unexpected wrapped next name: got %q want %q", name, filepath.Base(videoOne))
	}
	if path != videoOne {
		t.Fatalf("unexpected wrapped next path: got %q want %q", path, videoOne)
	}
}

func TestGetNextAutoPlayMediaWrapsSameTypeOnly(t *testing.T) {
	dir := t.TempDir()
	audioOne := filepath.Join(dir, "01.mp3")
	videoOne := filepath.Join(dir, "02.mp4")
	audioTwo := filepath.Join(dir, "03.mp3")

	screen := newTraversalTestScreen(t, audioTwo)
	screen.SkinNextOnlySameTypes = true
	screen.SessionQueue = newSessionQueue(testQueueItems(audioOne, videoOne, audioTwo), 2)

	name, path, err := getNextAutoPlayMediaOrError(screen)
	if err != nil {
		t.Fatalf("getNextAutoPlayMediaOrError failed: %v", err)
	}
	if name != filepath.Base(audioOne) {
		t.Fatalf("unexpected same-type wrapped next name: got %q want %q", name, filepath.Base(audioOne))
	}
	if path != audioOne {
		t.Fatalf("unexpected same-type wrapped next path: got %q want %q", path, audioOne)
	}
}

func TestGetNextAutoPlayMediaStopsWithoutWrapCandidate(t *testing.T) {
	dir := t.TempDir()
	videoOne := filepath.Join(dir, "01.mp4")
	audioOne := filepath.Join(dir, "02.mp3")

	screen := newTraversalTestScreen(t, videoOne)
	screen.SkinNextOnlySameTypes = true
	screen.SessionQueue = newSessionQueue(testQueueItems(videoOne, audioOne), 0)

	_, _, err := getNextAutoPlayMediaOrError(screen)
	if !errors.Is(err, errNoNextQueueMedia) {
		t.Fatalf("expected errNoNextQueueMedia, got %v", err)
	}
}

func TestGetAdjacentQueuedMediaRequiresQueue(t *testing.T) {
	screen := newTraversalTestScreen(t, "/tmp/test.mp4")

	_, _, err := getAdjacentMedia(screen, 1)
	if err == nil {
		t.Fatalf("expected error without queue")
	}
}

func TestGetPreviousQueuedMediaSameTypeOnly(t *testing.T) {
	dir := t.TempDir()
	videoOne := filepath.Join(dir, "01.mp4")
	audioOne := filepath.Join(dir, "02.mp3")
	videoTwo := filepath.Join(dir, "03.mp4")

	screen := newTraversalTestScreen(t, videoTwo)
	screen.SkinNextOnlySameTypes = true
	screen.SessionQueue = newSessionQueue(testQueueItems(videoOne, audioOne, videoTwo), 2)

	name, path, err := getAdjacentMedia(screen, -1)
	if err != nil {
		t.Fatalf("getAdjacentMedia failed: %v", err)
	}
	if name != filepath.Base(videoOne) {
		t.Fatalf("unexpected previous name: got %q want %q", name, filepath.Base(videoOne))
	}
	if path != videoOne {
		t.Fatalf("unexpected previous path: got %q want %q", path, videoOne)
	}
}

func TestSetAutoPlaySameTypesRefreshesTraversalControls(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	dir := t.TempDir()
	videoOne := filepath.Join(dir, "01.mp4")
	audioOne := filepath.Join(dir, "02.mp3")
	imageOne := filepath.Join(dir, "03.jpg")

	screen := newTraversalTestScreen(t, audioOne)
	screen.ExternalMediaURL = widget.NewCheck("", nil)
	screen.SkipPreviousButton = widget.NewButton("", nil)
	screen.SkipNextButton = widget.NewButton("", nil)
	screen.SkinNextOnlySameTypes = true
	screen.SessionQueue = newSessionQueue(testQueueItems(videoOne, audioOne, imageOne), 0)

	screen.refreshTraversalControls()
	fyne.DoAndWait(func() {})

	if !screen.SkipPreviousButton.Disabled() {
		t.Fatal("expected previous disabled with same-type traversal")
	}
	if !screen.SkipNextButton.Disabled() {
		t.Fatal("expected next disabled with same-type traversal")
	}

	screen.setAutoPlaySameTypes(false)
	fyne.DoAndWait(func() {})

	if screen.SkipPreviousButton.Disabled() {
		t.Fatal("expected previous enabled after disabling same-type traversal")
	}
	if screen.SkipNextButton.Disabled() {
		t.Fatal("expected next enabled after disabling same-type traversal")
	}
}

func TestSetAutoPlaySameTypesKeepsNextDisabledAtEnd(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	dir := t.TempDir()
	videoOne := filepath.Join(dir, "01.mp4")
	imageOne := filepath.Join(dir, "02.jpg")
	audioOne := filepath.Join(dir, "03.mp3")

	screen := newTraversalTestScreen(t, audioOne)
	screen.ExternalMediaURL = widget.NewCheck("", nil)
	screen.SkipPreviousButton = widget.NewButton("", nil)
	screen.SkipNextButton = widget.NewButton("", nil)
	screen.SkinNextOnlySameTypes = true
	screen.SessionQueue = newSessionQueue(testQueueItems(videoOne, imageOne, audioOne), 0)

	screen.setAutoPlaySameTypes(false)
	fyne.DoAndWait(func() {})

	if screen.SkipPreviousButton.Disabled() {
		t.Fatal("expected previous enabled after disabling same-type traversal")
	}
	if !screen.SkipNextButton.Disabled() {
		t.Fatal("expected next disabled at end of playlist")
	}
}

func TestClearCurrentMediaSelectionPreservesQueue(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	screen := &FyneScreen{
		State:              "Stopped",
		mediafile:          "/tmp/test.mp4",
		MediaText:          widget.NewEntry(),
		SelectInternalSubs: widget.NewSelect(nil, nil),
		PlayPause:          widget.NewButton("", nil),
		SessionQueue:       newSessionQueue(testQueueItems("/tmp/test.mp4"), 0),
	}

	screen.MediaText.SetText("test.mp4")
	clearCurrentMediaSelection(screen)

	if screen.SessionQueue == nil {
		t.Fatalf("expected queue to remain after media clear")
	}
	if screen.SessionQueue.CurrentIndex() != -1 {
		t.Fatalf("expected queue current index to be cleared, got %d", screen.SessionQueue.CurrentIndex())
	}
	if screen.mediafile != "" {
		t.Fatalf("expected mediafile to be cleared, got %q", screen.mediafile)
	}
}

func TestSelectMediaPathsSingleFileCreatesQueue(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	dir := t.TempDir()
	mediaPath := filepath.Join(dir, "single.mp4")
	screen := &FyneScreen{
		mediaFormats:       []string{".mp4"},
		videoFormats:       []string{".mp4"},
		MediaText:          widget.NewEntry(),
		SubsText:           widget.NewEntry(),
		SelectInternalSubs: widget.NewSelect(nil, nil),
		CustomSubsCheck:    widget.NewCheck("", nil),
		PlayPause:          widget.NewButton("", nil),
	}

	if err := selectMediaPaths(screen, []string{mediaPath}); err != nil {
		t.Fatalf("selectMediaPaths failed: %v", err)
	}
	fyne.DoAndWait(func() {})

	if screen.SessionQueue == nil {
		t.Fatalf("expected single media selection to create queue")
	}
	if screen.SessionQueue.Len() != 1 {
		t.Fatalf("expected single queue item, got %d", screen.SessionQueue.Len())
	}
	if screen.SessionQueue.CurrentIndex() != 0 {
		t.Fatalf("expected current queue index 0, got %d", screen.SessionQueue.CurrentIndex())
	}
	if screen.mediafile != mediaPath {
		t.Fatalf("expected mediafile %q, got %q", mediaPath, screen.mediafile)
	}
}

func TestSelectMediaPathsUppercaseExtensionCreatesQueue(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	dir := t.TempDir()
	mediaPath := filepath.Join(dir, "single.MKv")
	screen := &FyneScreen{
		mediaFormats:       []string{".mkv"},
		videoFormats:       []string{".mkv"},
		MediaText:          widget.NewEntry(),
		SubsText:           widget.NewEntry(),
		SelectInternalSubs: widget.NewSelect(nil, nil),
		CustomSubsCheck:    widget.NewCheck("", nil),
		PlayPause:          widget.NewButton("", nil),
	}

	if err := selectMediaPaths(screen, []string{mediaPath}); err != nil {
		t.Fatalf("selectMediaPaths failed: %v", err)
	}
	fyne.DoAndWait(func() {})

	if screen.SessionQueue == nil {
		t.Fatalf("expected mixed-case media selection to create queue")
	}
	if screen.SessionQueue.Len() != 1 {
		t.Fatalf("expected single queue item, got %d", screen.SessionQueue.Len())
	}
	item, _ := screen.SessionQueue.Item(0)
	if item.MediaKind() != "video" {
		t.Fatalf("expected video queue item, got %q", item.MediaKind())
	}
	if screen.mediafile != mediaPath {
		t.Fatalf("expected mediafile %q, got %q", mediaPath, screen.mediafile)
	}
	if screen.MediaText.Text != filepath.Base(mediaPath) {
		t.Fatalf("expected media text %q, got %q", filepath.Base(mediaPath), screen.MediaText.Text)
	}
	if screen.SessionQueue.CurrentIndex() != 0 {
		t.Fatalf("expected current queue index 0, got %d", screen.SessionQueue.CurrentIndex())
	}
}

func TestSelectMediaPathsSortsQueueByName(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	dir := t.TempDir()
	first := filepath.Join(dir, "01-first.mp4")
	second := filepath.Join(dir, "02-second.mp4")
	third := filepath.Join(dir, "03-third.mp4")
	screen := newQueueMediaSelectionTestScreen()

	if err := selectMediaPaths(screen, []string{third, first, second}); err != nil {
		t.Fatalf("selectMediaPaths failed: %v", err)
	}
	fyne.DoAndWait(func() {})

	assertQueuePaths(t, screen, []string{first, second, third})
	if screen.mediafile != first {
		t.Fatalf("expected current media %q, got %q", first, screen.mediafile)
	}
}

func TestAppendMediaPathsSortsAddedItems(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	dir := t.TempDir()
	existing := filepath.Join(dir, "00-existing.mp4")
	first := filepath.Join(dir, "01-first.mp4")
	second := filepath.Join(dir, "02-second.mp4")
	third := filepath.Join(dir, "03-third.mp4")
	screen := newQueueMediaSelectionTestScreen()
	screen.mediafile = existing
	screen.SessionQueue = newSessionQueue(testQueueItems(existing), 0)

	if err := appendMediaPaths(screen, []string{third, first, second}); err != nil {
		t.Fatalf("appendMediaPaths failed: %v", err)
	}
	fyne.DoAndWait(func() {})

	assertQueuePaths(t, screen, []string{existing, first, second, third})
	if screen.SessionQueue.CurrentIndex() != 0 {
		t.Fatalf("expected current queue index 0, got %d", screen.SessionQueue.CurrentIndex())
	}
}

func TestQueueInteractionsLocked(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	screen := &FyneScreen{
		ExternalMediaURL: widget.NewCheck("", nil),
		rtmpServerCheck:  widget.NewCheck("", nil),
	}

	if screen.queueInteractionsLocked() {
		t.Fatalf("expected queue to be unlocked initially")
	}

	screen.ExternalMediaURL.SetChecked(true)
	if !screen.queueInteractionsLocked() {
		t.Fatalf("expected queue lock for external media mode")
	}

	screen.ExternalMediaURL.SetChecked(false)
	screen.rtmpServerCheck.SetChecked(true)
	if !screen.queueInteractionsLocked() {
		t.Fatalf("expected queue lock for RTMP mode")
	}

	screen.rtmpServerCheck.SetChecked(false)
	screen.Screencast = true
	if !screen.queueInteractionsLocked() {
		t.Fatalf("expected queue lock for screencast mode")
	}
}

func TestActiveQueueIndexRequiresCurrentMedia(t *testing.T) {
	screen := &FyneScreen{}
	queue := newSessionQueue(testQueueItems("/tmp/one.mp4", "/tmp/two.mp4"), 1)

	if got := screen.activeQueueIndex(queue); got != -1 {
		t.Fatalf("expected no active queue item without media selection, got %d", got)
	}

	screen.mediafile = "/tmp/two.mp4"
	if got := screen.activeQueueIndex(queue); got != 1 {
		t.Fatalf("expected active queue index 1, got %d", got)
	}
}

func TestQueueDropMode(t *testing.T) {
	screen := &FyneScreen{}

	if got := screen.queueDropMode(); got != droppedMediaModeReplace {
		t.Fatalf("expected replace mode for empty queue, got %d", got)
	}

	screen.SessionQueue = newSessionQueue(testQueueItems("/tmp/one.mp4"), 0)

	if got := screen.queueDropMode(); got != droppedMediaModeAppend {
		t.Fatalf("expected append mode for existing queue, got %d", got)
	}
}

func TestQueueWindowUnavailableDuringRemoteSession(t *testing.T) {
	screen := &FyneScreen{}
	releaseLease, err := screen.renderGate.acquireRemoteLease()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLease()

	screen.openQueueWindow()
	if screen.queueWindow != nil {
		t.Fatal("playlist window created during remote session")
	}
}

func TestDroppedMediaBlockedErrorForAppendMode(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	screen := &FyneScreen{
		ExternalMediaURL: widget.NewCheck("", nil),
		rtmpServerCheck:  widget.NewCheck("", nil),
	}

	if err := screen.droppedMediaBlockedErrorForMode(droppedMediaModeAppend); err != nil {
		t.Fatalf("unexpected append drop block without live mode: %v", err)
	}

	screen.ExternalMediaURL.SetChecked(true)
	if err := screen.droppedMediaBlockedErrorForMode(droppedMediaModeAppend); err == nil {
		t.Fatalf("expected append drop block while external URL mode is active")
	}

	if err := screen.droppedMediaBlockedErrorForMode(droppedMediaModeReplace); err != nil {
		t.Fatalf("replace mode should still allow dropping files, got %v", err)
	}
}

func TestRemoveSelectedQueueItemClearsCurrentOnLastRemove(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	mediaPath := "/tmp/test.mp4"
	screen := &FyneScreen{
		mediafile:          mediaPath,
		queueSelectedIndex: 0,
		MediaText:          widget.NewEntry(),
		SelectInternalSubs: widget.NewSelect(nil, nil),
		CustomSubsCheck:    widget.NewCheck("", nil),
		PlayPause:          widget.NewButton("", nil),
		SessionQueue:       newSessionQueue(testQueueItems(mediaPath), 0),
	}

	screen.removeSelectedQueueItem()

	if screen.SessionQueue != nil {
		t.Fatalf("expected queue to be cleared after removing last item")
	}
	if screen.mediafile != "" {
		t.Fatalf("expected media selection to be cleared, got %q", screen.mediafile)
	}
}

func TestClearSessionQueueActionClearsCurrentMedia(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	mediaPath := "/tmp/test.mp4"
	screen := &FyneScreen{
		mediafile:          mediaPath,
		MediaText:          widget.NewEntry(),
		SelectInternalSubs: widget.NewSelect(nil, nil),
		CustomSubsCheck:    widget.NewCheck("", nil),
		PlayPause:          widget.NewButton("", nil),
		SessionQueue:       newSessionQueue(testQueueItems(mediaPath), 0),
	}

	screen.clearSessionQueueAction()

	if screen.SessionQueue != nil {
		t.Fatalf("expected queue to be cleared")
	}
	if screen.mediafile != "" {
		t.Fatalf("expected media selection to be cleared, got %q", screen.mediafile)
	}
}

func TestSingleItemPlaylistButtonStaysNeutral(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	mediaPath := "/tmp/test.mp4"
	screen := &FyneScreen{
		mediafile:    mediaPath,
		QueueButton:  widget.NewButton("", nil),
		SessionQueue: newSessionQueue(testQueueItems(mediaPath), 0),
	}

	screen.refreshQueueStateUI()
	fyne.DoAndWait(func() {})

	if screen.QueueButton.Importance != widget.MediumImportance {
		t.Fatalf("expected single-item playlist button to stay neutral, got %v", screen.QueueButton.Importance)
	}
}

func TestSingleActiveQueueItemRemoveButtonEnabled(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	mediaPath := "/tmp/test.mp4"
	screen := &FyneScreen{
		mediafile:          mediaPath,
		queueSelectedIndex: 0,
		queueRemoveButton:  widget.NewButton("", nil),
		SessionQueue:       newSessionQueue(testQueueItems(mediaPath), 0),
	}

	screen.refreshQueueStateUI()
	fyne.DoAndWait(func() {})

	if screen.queueRemoveButton.Disabled() {
		t.Fatalf("expected remove button enabled for single active queue item")
	}
}

func TestMultiItemPlaylistButtonTurnsProminent(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	mediaPath := "/tmp/one.mp4"
	screen := &FyneScreen{
		mediafile:    mediaPath,
		QueueButton:  widget.NewButton("", nil),
		SessionQueue: newSessionQueue(testQueueItems(mediaPath, "/tmp/two.mp4"), 0),
	}

	screen.refreshQueueStateUI()
	fyne.DoAndWait(func() {})

	if screen.QueueButton.Importance != widget.HighImportance {
		t.Fatalf("expected multi-item playlist button to become prominent, got %v", screen.QueueButton.Importance)
	}
}

func TestRecordQueueUIStateSkipsDuplicateRefreshes(t *testing.T) {
	screen := &FyneScreen{}
	listOne := widget.NewList(
		func() int { return 0 },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(widget.ListItemID, fyne.CanvasObject) {},
	)
	listTwo := widget.NewList(
		func() int { return 0 },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(widget.ListItemID, fyne.CanvasObject) {},
	)

	state := queueUIState{
		revision:         7,
		queueLen:         32,
		selectedIndex:    2,
		activeIndex:      2,
		buttonText:       "Playlist 3/32",
		buttonImportance: widget.HighImportance,
		statusText:       "Playlist 3/32",
		detailsText:      "/tmp/three.mp4",
		list:             listOne,
	}

	if !screen.recordQueueUIState(state) {
		t.Fatal("expected first queue UI state to be recorded")
	}
	if screen.recordQueueUIState(state) {
		t.Fatal("expected identical queue UI state to be skipped")
	}

	state.revision++
	if !screen.recordQueueUIState(state) {
		t.Fatal("expected queue revision change to force refresh")
	}

	state.list = listTwo
	if !screen.recordQueueUIState(state) {
		t.Fatal("expected new queue list instance to force refresh")
	}
}

func TestQueueSelectionDetailsShowsFullPath(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	item := testQueueItems(filepath.Join(t.TempDir(), "movie.mp4"))[0]
	screen := &FyneScreen{
		queueDetails:       widget.NewLabel(""),
		queueSelectedIndex: 0,
		SessionQueue:       newSessionQueue([]QueueItem{item}, 0),
	}

	screen.refreshQueueStateUI()
	fyne.DoAndWait(func() {})

	if screen.queueDetails.Text != item.DisplayPath() {
		t.Fatalf("selection details = %q, want %q", screen.queueDetails.Text, item.DisplayPath())
	}
}

func TestQueueRowDedupesThumbnailRequests(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.mp4")
	secondPath := filepath.Join(dir, "second.mp4")

	row := newQueueRow(&FyneScreen{
		Debug: newDebugWriter(8),
	})

	first := testQueueItems(firstPath)[0]
	second := testQueueItems(secondPath)[0]

	row.setRow(0, first, false)
	firstRequestID := row.thumbnailRequestID
	if row.pendingThumbPath != first.Path() {
		t.Fatalf("expected pending thumbnail path %q, got %q", first.Path(), row.pendingThumbPath)
	}

	row.setRow(0, first, false)
	if row.thumbnailRequestID != firstRequestID {
		t.Fatalf("expected duplicate pending thumbnail request to be skipped, got request id %d want %d", row.thumbnailRequestID, firstRequestID)
	}

	row.setRow(0, second, false)
	if row.thumbnailRequestID == firstRequestID {
		t.Fatal("expected path change to invalidate and replace pending thumbnail request")
	}
	if row.pendingThumbPath != second.Path() {
		t.Fatalf("expected pending thumbnail path %q after path change, got %q", second.Path(), row.pendingThumbPath)
	}
}
