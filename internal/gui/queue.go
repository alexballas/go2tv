//go:build !(android || ios)

package gui

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/canvas"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/layout"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	xfilepicker "github.com/alexballas/xfilepicker/dialog"
)

const (
	queueRowThumbWidth  float32 = 96
	queueRowThumbHeight float32 = 60
)

type queueThumbLayout struct{}

type queueRowRenderer struct {
	row     *queueRow
	objects []fyne.CanvasObject
}

type QueueItem struct {
	Path         string
	DisplayPath  string
	BaseName     string
	ParentFolder string
	MediaType    string
}

func (item QueueItem) displayPath() string {
	if item.DisplayPath != "" {
		return item.DisplayPath
	}

	return item.Path
}

type SessionQueue struct {
	Items        []QueueItem
	CurrentIndex int
}

type queueUIState struct {
	revision         uint64
	queueLen         int
	selectedIndex    int
	activeIndex      int
	buttonText       string
	buttonImportance widget.Importance
	statusText       string
	detailsText      string
	locked           bool
	list             *widget.List
}

func newSessionQueue(items []QueueItem, currentIndex int) *SessionQueue {
	if len(items) == 0 {
		return nil
	}

	cloned := make([]QueueItem, len(items))
	copy(cloned, items)

	if currentIndex < -1 || currentIndex >= len(cloned) {
		currentIndex = 0
	}

	return &SessionQueue{
		Items:        cloned,
		CurrentIndex: currentIndex,
	}
}

func (q *SessionQueue) clone() *SessionQueue {
	if q == nil {
		return nil
	}

	return newSessionQueue(q.Items, q.CurrentIndex)
}

func (q *SessionQueue) indexByPath(mediaPath string) int {
	if q == nil {
		return -1
	}

	return slices.IndexFunc(q.Items, func(item QueueItem) bool {
		return item.Path == mediaPath
	})
}

func (q *SessionQueue) setCurrentByPath(mediaPath string) bool {
	if q == nil {
		return false
	}

	idx := q.indexByPath(mediaPath)
	if idx == -1 {
		return false
	}

	q.CurrentIndex = idx
	return true
}

func (q *SessionQueue) adjacentIndex(delta int, sameTypeOnly, wrap bool) int {
	if q == nil || q.CurrentIndex < 0 || q.CurrentIndex >= len(q.Items) {
		return -1
	}

	targetType := q.Items[q.CurrentIndex].MediaType
	matches := func(idx int) bool {
		return !sameTypeOnly || q.Items[idx].MediaType == targetType
	}

	for idx := q.CurrentIndex + delta; idx >= 0 && idx < len(q.Items); idx += delta {
		if matches(idx) {
			return idx
		}
	}

	if !wrap {
		return -1
	}

	switch {
	case delta > 0:
		for idx := 0; idx < q.CurrentIndex; idx++ {
			if matches(idx) {
				return idx
			}
		}
	case delta < 0:
		for idx := len(q.Items) - 1; idx > q.CurrentIndex; idx-- {
			if matches(idx) {
				return idx
			}
		}
	}

	return -1
}

func (q *SessionQueue) move(index, delta int) int {
	if q == nil || index < 0 || index >= len(q.Items) {
		return -1
	}

	target := index + delta
	if target < 0 || target >= len(q.Items) {
		return index
	}

	q.Items[index], q.Items[target] = q.Items[target], q.Items[index]

	switch q.CurrentIndex {
	case index:
		q.CurrentIndex = target
	case target:
		q.CurrentIndex = index
	}

	return target
}

func (screen *FyneScreen) mediaKindForPath(mediaPath string) string {
	ext := strings.ToLower(filepath.Ext(mediaPath))

	switch {
	case slices.Contains(screen.imageFormats, ext):
		return "image"
	case slices.Contains(screen.videoFormats, ext):
		return "video"
	case slices.Contains(screen.audioFormats, ext):
		return "audio"
	default:
		return ""
	}
}

func (screen *FyneScreen) newQueueItem(mediaPath string) (QueueItem, bool) {
	absPath, err := filepath.Abs(mediaPath)
	if err != nil {
		return QueueItem{}, false
	}

	mediaType := screen.mediaKindForPath(absPath)
	if mediaType == "" {
		return QueueItem{}, false
	}

	displayPath := queueDisplayPath(absPath)

	return QueueItem{
		Path:         absPath,
		DisplayPath:  displayPath,
		BaseName:     filepath.Base(displayPath),
		ParentFolder: filepath.Dir(displayPath),
		MediaType:    mediaType,
	}, true
}

func (screen *FyneScreen) buildQueueItems(paths []string) []QueueItem {
	items := make([]QueueItem, 0, len(paths))
	for _, mediaPath := range sortedMediaPaths(paths) {
		item, ok := screen.newQueueItem(mediaPath)
		if !ok {
			continue
		}
		items = append(items, item)
	}

	return items
}

func sortedMediaPaths(paths []string) []string {
	sorted := slices.Clone(paths)
	slices.SortFunc(sorted, compareMediaPaths)
	return sorted
}

func compareMediaPaths(a, b string) int {
	aName := strings.ToLower(filepath.Base(filepath.Clean(a)))
	bName := strings.ToLower(filepath.Base(filepath.Clean(b)))
	if cmp := strings.Compare(aName, bName); cmp != 0 {
		return cmp
	}

	aPath := strings.ToLower(filepath.Clean(a))
	bPath := strings.ToLower(filepath.Clean(b))
	if cmp := strings.Compare(aPath, bPath); cmp != 0 {
		return cmp
	}

	return strings.Compare(filepath.Clean(a), filepath.Clean(b))
}

func (screen *FyneScreen) bumpQueueRevisionLocked() {
	screen.queueRevision++
}

func (screen *FyneScreen) replaceSessionQueue(items []QueueItem, currentIndex int) {
	screen.mu.Lock()
	if len(items) == 0 {
		screen.SessionQueue = nil
		screen.queueSelectedIndex = -1
	} else {
		screen.SessionQueue = newSessionQueue(items, currentIndex)
		if currentIndex >= 0 && currentIndex < len(items) {
			screen.queueSelectedIndex = currentIndex
		} else {
			screen.queueSelectedIndex = 0
		}
	}
	screen.bumpQueueRevisionLocked()
	screen.mu.Unlock()
	screen.prewarmQueueThumbnails(items)
	screen.refreshQueueStateUI()
}

func (screen *FyneScreen) prewarmQueueThumbnails(items []QueueItem) {
	if len(items) == 0 {
		return
	}

	uris := make([]fyne.URI, 0, len(items))
	audioPaths := make([]string, 0, len(items))
	for _, item := range items {
		switch item.MediaType {
		case "audio":
			audioPaths = append(audioPaths, item.Path)
		case "image", "video":
			uris = append(uris, storage.NewFileURI(item.Path))
		}
	}

	if len(uris) > 0 {
		xfilepicker.GetThumbnailManager().PrewarmDirectory(uris)
	}
	if len(audioPaths) > 0 {
		go func() {
			for _, path := range audioPaths {
				screen.resolveCachedGUIArtwork(path, "audio/unknown", true)
			}
		}()
	}
}

func (screen *FyneScreen) queueAudioThumbnail(path string) *canvas.Image {
	_, asset := screen.resolveCachedGUIArtwork(path, "audio/unknown", true)
	if asset == nil {
		return nil
	}

	artwork, err := jpeg.Decode(bytes.NewReader(asset.Data))
	if err != nil {
		return nil
	}
	thumbnail := canvas.NewImageFromImage(artwork)
	thumbnail.FillMode = canvas.ImageFillContain
	return thumbnail
}

func queueItemNeedsThumbnail(mediaType string) bool {
	return mediaType == "audio" || mediaType == "image" || mediaType == "video"
}

func (screen *FyneScreen) queueSnapshot() (*SessionQueue, int) {
	screen.mu.RLock()
	defer screen.mu.RUnlock()

	return screen.SessionQueue.clone(), screen.queueSelectedIndex
}

func (screen *FyneScreen) queueRenderSnapshot() (*SessionQueue, int, uint64, *widget.List) {
	screen.mu.RLock()
	defer screen.mu.RUnlock()

	return screen.SessionQueue.clone(), screen.queueSelectedIndex, screen.queueRevision, screen.queueList
}

func (screen *FyneScreen) queueItemCount() int {
	screen.mu.RLock()
	defer screen.mu.RUnlock()

	if screen.SessionQueue == nil {
		return 0
	}

	return len(screen.SessionQueue.Items)
}

func (screen *FyneScreen) queueItemForList(index int) (QueueItem, bool) {
	screen.mu.RLock()
	defer screen.mu.RUnlock()

	if screen.SessionQueue == nil || index < 0 || index >= len(screen.SessionQueue.Items) {
		return QueueItem{}, false
	}

	return screen.SessionQueue.Items[index], screen.mediafile == screen.SessionQueue.Items[index].Path
}

func (screen *FyneScreen) hasSessionQueue() bool {
	screen.mu.RLock()
	defer screen.mu.RUnlock()

	return screen.SessionQueue != nil && len(screen.SessionQueue.Items) > 0
}

func (screen *FyneScreen) syncQueueCurrentWithMedia(mediaPath string) {
	screen.mu.Lock()
	defer screen.mu.Unlock()

	if screen.SessionQueue == nil {
		return
	}

	if screen.SessionQueue.setCurrentByPath(mediaPath) {
		screen.queueSelectedIndex = screen.SessionQueue.CurrentIndex
		screen.bumpQueueRevisionLocked()
	}
}

func (screen *FyneScreen) clearQueueCurrent() {
	screen.mu.Lock()
	defer screen.mu.Unlock()

	if screen.SessionQueue == nil {
		return
	}

	screen.SessionQueue.CurrentIndex = -1
	screen.bumpQueueRevisionLocked()
}

func (screen *FyneScreen) setQueueSelectedIndex(index int) {
	screen.mu.Lock()
	screen.queueSelectedIndex = index
	screen.mu.Unlock()

	screen.refreshQueueStateUI()
}

func (screen *FyneScreen) activeQueueIndex(queue *SessionQueue) int {
	if queue == nil || len(queue.Items) == 0 || screen.mediafile == "" {
		return -1
	}

	return queue.indexByPath(screen.mediafile)
}

func (screen *FyneScreen) queueStatusText(queue *SessionQueue, activeIndex int) string {
	if activeIndex >= 0 && activeIndex < len(queue.Items) {
		return fmt.Sprintf(lang.L("Playlist %d/%d"), activeIndex+1, len(queue.Items))
	}

	return fmt.Sprintf(lang.L("Playlist: %d items"), len(queue.Items))
}

func (screen *FyneScreen) queueButtonText(queue *SessionQueue, activeIndex int) string {
	if queue == nil || len(queue.Items) == 0 {
		return lang.L("Playlist")
	}

	if activeIndex >= 0 && activeIndex < len(queue.Items) {
		return fmt.Sprintf(lang.L("Playlist %d/%d"), activeIndex+1, len(queue.Items))
	}

	return fmt.Sprintf(lang.L("Playlist %d"), len(queue.Items))
}

func (screen *FyneScreen) queueInteractionsLocked() bool {
	return screen.Screencast ||
		(screen.rtmpServerCheck != nil && screen.rtmpServerCheck.Checked) ||
		(screen.ExternalMediaURL != nil && screen.ExternalMediaURL.Checked)
}

func (screen *FyneScreen) refreshQueueStateUI() {
	queue, selectedIndex, queueRevision, queueList := screen.queueRenderSnapshot()
	activeIndex := screen.activeQueueIndex(queue)
	statusText := ""
	buttonText := screen.queueButtonText(queue, activeIndex)
	buttonImportance := widget.MediumImportance
	detailsText := lang.L("No item selected")
	locked := screen.queueInteractionsLocked()

	if queue != nil && selectedIndex >= 0 && selectedIndex < len(queue.Items) {
		detailsText = queue.Items[selectedIndex].displayPath()
	}
	if queue != nil && len(queue.Items) > 0 {
		statusText = screen.queueStatusText(queue, activeIndex)
		buttonText = statusText
		if len(queue.Items) > 1 {
			buttonImportance = widget.HighImportance
		}
	}

	queueLen := 0
	if queue != nil {
		queueLen = len(queue.Items)
	}

	state := queueUIState{
		revision:         queueRevision,
		queueLen:         queueLen,
		selectedIndex:    selectedIndex,
		activeIndex:      activeIndex,
		buttonText:       buttonText,
		buttonImportance: buttonImportance,
		statusText:       statusText,
		detailsText:      detailsText,
		locked:           locked,
		list:             queueList,
	}
	if !screen.recordQueueUIState(state) {
		screen.refreshTraversalControls()
		return
	}

	fyne.Do(func() {
		if screen.QueueButton != nil {
			screen.QueueButton.SetText(buttonText)
			screen.QueueButton.Importance = buttonImportance
			screen.QueueButton.Refresh()
		}

		if screen.queueHeader != nil {
			if queue == nil || len(queue.Items) == 0 {
				screen.queueHeader.SetText(lang.L("Playlist is empty"))
			} else {
				screen.queueHeader.SetText(statusText)
			}
		}

		if screen.queueDetails != nil {
			screen.queueDetails.SetText(detailsText)
		}

		if screen.queueList != nil {
			screen.queueList.Refresh()
			onSelected := screen.queueList.OnSelected
			onUnselected := screen.queueList.OnUnselected
			screen.queueList.OnSelected = nil
			screen.queueList.OnUnselected = nil
			if queue != nil && selectedIndex >= 0 && selectedIndex < len(queue.Items) {
				screen.queueList.Select(selectedIndex)
			} else {
				screen.queueList.UnselectAll()
			}
			screen.queueList.OnSelected = onSelected
			screen.queueList.OnUnselected = onUnselected
		}

		currentSelected := queue != nil && selectedIndex >= 0 && selectedIndex < len(queue.Items)
		currentIsActive := currentSelected && activeIndex == selectedIndex

		if screen.queueAddButton != nil {
			if !locked {
				screen.queueAddButton.Enable()
			} else {
				screen.queueAddButton.Disable()
			}
		}

		if screen.queuePlayNowButton != nil {
			if currentSelected && !locked {
				screen.queuePlayNowButton.Enable()
			} else {
				screen.queuePlayNowButton.Disable()
			}
		}

		if screen.queueRemoveButton != nil {
			allowActiveRemove := queue != nil && len(queue.Items) == 1
			if currentSelected && (!currentIsActive || allowActiveRemove) && !locked {
				screen.queueRemoveButton.Enable()
			} else {
				screen.queueRemoveButton.Disable()
			}
		}

		if screen.queueMoveUpButton != nil {
			if currentSelected && selectedIndex > 0 && !locked {
				screen.queueMoveUpButton.Enable()
			} else {
				screen.queueMoveUpButton.Disable()
			}
		}

		if screen.queueMoveDownButton != nil {
			if currentSelected && queue != nil && selectedIndex < len(queue.Items)-1 && !locked {
				screen.queueMoveDownButton.Enable()
			} else {
				screen.queueMoveDownButton.Disable()
			}
		}

		if screen.queueClearButton != nil {
			if queue != nil && len(queue.Items) > 0 && !locked {
				screen.queueClearButton.Enable()
			} else {
				screen.queueClearButton.Disable()
			}
		}
	})

	screen.refreshTraversalControls()
}

func (screen *FyneScreen) recordQueueUIState(state queueUIState) bool {
	screen.mu.Lock()
	defer screen.mu.Unlock()

	if screen.queueUIStateValid && screen.lastQueueUIState == state {
		return false
	}

	screen.lastQueueUIState = state
	screen.queueUIStateValid = true
	return true
}

func (screen *FyneScreen) scrollQueueListToBottom() {
	fyne.Do(func() {
		if screen.queueList != nil {
			screen.queueList.ScrollToBottom()
		}
	})
}

func (screen *FyneScreen) canTraverse(delta int) bool {
	if screen.mediafile == "" {
		return false
	}
	if screen.ExternalMediaURL != nil && screen.ExternalMediaURL.Checked {
		return false
	}

	_, _, err := getAdjacentMedia(screen, delta)
	return err == nil
}

func (screen *FyneScreen) refreshTraversalControls() {
	previousEnabled := screen.canTraverse(-1)
	nextEnabled := screen.canTraverse(1)

	fyne.Do(func() {
		if screen.SkipPreviousButton != nil {
			if previousEnabled {
				screen.SkipPreviousButton.Enable()
			} else {
				screen.SkipPreviousButton.Disable()
			}
		}

		if screen.SkipNextButton != nil {
			if nextEnabled {
				screen.SkipNextButton.Enable()
			} else {
				screen.SkipNextButton.Disable()
			}
		}
	})
}

func (screen *FyneScreen) openQueueWindow() {
	if screen.queueWindow == nil {
		screen.buildQueueWindow()
	}

	if screen.queueWindow != nil {
		screen.queueWindow.CenterOnScreen()
		screen.queueWindow.Show()
	}

	screen.refreshQueueStateUI()
}

func (screen *FyneScreen) queueDropMode() droppedMediaMode {
	if screen.hasSessionQueue() {
		return droppedMediaModeAppend
	}

	return droppedMediaModeReplace
}

func onQueueDropFiles(screen *FyneScreen) func(p fyne.Position, u []fyne.URI) {
	return func(p fyne.Position, u []fyne.URI) {
		handleDroppedFiles(screen, screen.queueDropMode(), u)
	}
}

func (screen *FyneScreen) buildQueueWindow() {
	win := fyne.CurrentApp().NewWindow(lang.L("Playlist"))
	win.SetOnDropped(onQueueDropFiles(screen))
	header := widget.NewLabel("")
	details := widget.NewLabel(lang.L("No item selected"))
	details.Wrapping = fyne.TextWrapWord

	list := widget.NewList(
		func() int {
			return screen.queueItemCount()
		},
		func() fyne.CanvasObject {
			return newQueueRow(screen)
		},
		func(id widget.ListItemID, object fyne.CanvasObject) {
			row := object.(*queueRow)
			item, isCurrent := screen.queueItemForList(id)
			if item.Path == "" {
				row.setRow(id, QueueItem{}, false)
				return
			}

			row.setRow(id, item, isCurrent)
		},
	)
	list.OnSelected = func(id widget.ListItemID) {
		screen.setQueueSelectedIndex(id)
	}
	list.OnUnselected = func(widget.ListItemID) {
		screen.setQueueSelectedIndex(-1)
	}

	addFiles := widget.NewButton(lang.L("Add files"), func() {
		parent := screen.Current
		if screen.queueWindow != nil {
			parent = screen.queueWindow
		}
		openMediaPickerForWindow(screen, parent, appendMediaPaths, nil)
	})
	selectItem := widget.NewButton(lang.L("Select"), func() {
		screen.activateSelectedQueueItem()
	})
	remove := widget.NewButton(lang.L("Remove"), func() {
		screen.removeSelectedQueueItem()
	})
	moveUp := widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
		screen.moveSelectedQueueItem(-1)
	})
	moveDown := widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() {
		screen.moveSelectedQueueItem(1)
	})
	clearQueue := widget.NewButton(lang.L("Clear playlist"), func() {
		screen.clearSessionQueueAction()
	})
	closeButton := widget.NewButton(lang.L("Close"), func() {
		win.Close()
	})

	buttons := container.NewHBox(
		addFiles,
		selectItem,
		remove,
		moveUp,
		moveDown,
		layout.NewSpacer(),
		clearQueue,
		closeButton,
	)

	win.SetContent(container.NewBorder(
		container.NewVBox(header),
		container.NewVBox(widget.NewSeparator(), details, buttons),
		nil,
		nil,
		list,
	))
	win.Resize(fyne.NewSize(760, 420))
	win.SetOnClosed(func() {
		screen.queueWindow = nil
		screen.queueList = nil
		screen.queueHeader = nil
		screen.queueDetails = nil
		screen.queueAddButton = nil
		screen.queuePlayNowButton = nil
		screen.queueRemoveButton = nil
		screen.queueMoveUpButton = nil
		screen.queueMoveDownButton = nil
		screen.queueClearButton = nil
	})
	win.Canvas().SetOnTypedKey(func(key *fyne.KeyEvent) {
		switch key.Name {
		case fyne.KeyReturn, fyne.KeyEnter:
			screen.activateSelectedQueueItem()
		}
	})

	screen.queueWindow = win
	screen.queueList = list
	screen.queueHeader = header
	screen.queueDetails = details
	screen.queueAddButton = addFiles
	screen.queuePlayNowButton = selectItem
	screen.queueRemoveButton = remove
	screen.queueMoveUpButton = moveUp
	screen.queueMoveDownButton = moveDown
	screen.queueClearButton = clearQueue
}

func (screen *FyneScreen) activateSelectedQueueItem() {
	if screen.queueInteractionsLocked() {
		return
	}

	queue, selectedIndex := screen.queueSnapshot()
	if queue == nil || selectedIndex < 0 || selectedIndex >= len(queue.Items) {
		return
	}

	item := queue.Items[selectedIndex]
	if item.Path == screen.mediafile {
		screen.setQueueSelectedIndex(selectedIndex)
		return
	}

	if screen.getScreenState() == "Playing" || screen.getScreenState() == "Paused" {
		skipToMediaPathAction(screen, item.Path)
		return
	}

	if err := setCurrentMediaPath(screen, item.Path); err != nil {
		check(screen, err)
	}
}

func (screen *FyneScreen) handleQueueRowTap(index int) {
	now := time.Now()
	activate := false

	screen.mu.Lock()
	if screen.lastQueueTapIndex == index && now.Sub(screen.lastQueueTapAt) <= 400*time.Millisecond {
		activate = true
	}
	screen.lastQueueTapIndex = index
	screen.lastQueueTapAt = now
	screen.queueSelectedIndex = index
	screen.mu.Unlock()
	screen.refreshQueueStateUI()
	if activate {
		screen.activateSelectedQueueItem()
	}
}

func (screen *FyneScreen) removeSelectedQueueItem() {
	screen.mu.Lock()
	if screen.SessionQueue == nil || screen.queueSelectedIndex < 0 || screen.queueSelectedIndex >= len(screen.SessionQueue.Items) {
		screen.mu.Unlock()
		return
	}

	selectedIndex := screen.queueSelectedIndex
	selectedItem := screen.SessionQueue.Items[selectedIndex]
	currentIsActive := screen.mediafile != "" && selectedItem.Path == screen.mediafile
	if currentIsActive && len(screen.SessionQueue.Items) > 1 {
		screen.mu.Unlock()
		check(screen, fmt.Errorf("%s", lang.L("cannot remove the current queue item")))
		return
	}

	screen.SessionQueue.Items = append(
		screen.SessionQueue.Items[:selectedIndex],
		screen.SessionQueue.Items[selectedIndex+1:]...,
	)
	screen.bumpQueueRevisionLocked()

	if len(screen.SessionQueue.Items) == 0 {
		screen.SessionQueue = nil
		screen.queueSelectedIndex = -1
		screen.mu.Unlock()
		if currentIsActive {
			clearCurrentMediaSelection(screen)
			return
		}
		screen.refreshQueueStateUI()
		return
	}

	if screen.queueSelectedIndex >= len(screen.SessionQueue.Items) {
		screen.queueSelectedIndex = len(screen.SessionQueue.Items) - 1
	}
	if screen.mediafile == "" {
		screen.SessionQueue.CurrentIndex = -1
	} else if currentIndex := screen.SessionQueue.indexByPath(screen.mediafile); currentIndex != -1 {
		screen.SessionQueue.CurrentIndex = currentIndex
	}
	screen.mu.Unlock()

	screen.refreshQueueStateUI()
}

func (screen *FyneScreen) moveSelectedQueueItem(delta int) {
	screen.mu.Lock()
	if screen.SessionQueue == nil || screen.queueSelectedIndex < 0 || screen.queueSelectedIndex >= len(screen.SessionQueue.Items) {
		screen.mu.Unlock()
		return
	}

	screen.queueSelectedIndex = screen.SessionQueue.move(screen.queueSelectedIndex, delta)
	screen.bumpQueueRevisionLocked()
	screen.mu.Unlock()

	screen.refreshQueueStateUI()
}

func (screen *FyneScreen) clearSessionQueueAction() {
	screen.replaceSessionQueue(nil, -1)
	clearCurrentMediaSelection(screen)
}

type queueRow struct {
	widget.BaseWidget
	screen             *FyneScreen
	index              int
	currentPath        string
	thumbPath          string
	pendingThumbPath   string
	thumbnailRequestID uint64
	thumbnail          *canvas.Image
	fallbackIcon       *canvas.Image
	title              *widget.Label
	subtitle           *widget.Label
	currentIcon        *widget.Icon
	content            fyne.CanvasObject
}

func newQueueRow(screen *FyneScreen) *queueRow {
	thumbnail := canvas.NewImageFromImage(nil)
	thumbnail.FillMode = canvas.ImageFillContain
	thumbnail.Hide()

	fallbackIcon := canvas.NewImageFromResource(theme.FileVideoIcon())
	fallbackIcon.FillMode = canvas.ImageFillContain

	title := widget.NewLabel("")
	title.Truncation = fyne.TextTruncateEllipsis

	subtitle := widget.NewLabel("")
	subtitle.Truncation = fyne.TextTruncateEllipsis

	thumb := container.New(
		queueThumbLayout{},
		container.NewStack(
			thumbnail,
			fallbackIcon,
		),
	)

	row := &queueRow{
		screen:       screen,
		thumbnail:    thumbnail,
		fallbackIcon: fallbackIcon,
		title:        title,
		subtitle:     subtitle,
		currentIcon:  widget.NewIcon(nil),
	}
	row.content = container.NewBorder(
		nil,
		nil,
		thumb,
		row.currentIcon,
		container.NewVBox(row.title, row.subtitle),
	)
	row.ExtendBaseWidget(row)
	return row
}

func (r *queueRow) CreateRenderer() fyne.WidgetRenderer {
	return &queueRowRenderer{
		row:     r,
		objects: []fyne.CanvasObject{r.content},
	}
}

func (r *queueRow) Tapped(*fyne.PointEvent) {
	if r.screen == nil {
		return
	}

	r.screen.handleQueueRowTap(r.index)
}

func (r *queueRow) setRow(index int, item QueueItem, isCurrent bool) {
	samePath := r.currentPath == item.Path
	r.index = index
	r.currentPath = item.Path
	r.title.SetText(item.BaseName)
	r.subtitle.SetText(item.ParentFolder)

	if !samePath {
		r.thumbnailRequestID++
		r.pendingThumbPath = ""
	}

	switch item.MediaType {
	case "audio":
		r.fallbackIcon.Resource = theme.FileAudioIcon()
	case "image":
		r.fallbackIcon.Resource = theme.FileImageIcon()
	case "video":
		r.fallbackIcon.Resource = theme.FileVideoIcon()
	default:
		r.fallbackIcon.Resource = theme.FileIcon()
	}

	needsThumb := queueItemNeedsThumbnail(item.MediaType)
	reuseThumb := samePath && r.thumbPath == item.Path && r.thumbnail.Image != nil

	if !reuseThumb {
		r.thumbnail.File = ""
		r.thumbnail.Resource = nil
		r.thumbnail.Image = nil
		r.thumbPath = ""
		r.thumbnail.Hide()
		r.fallbackIcon.Show()

		if needsThumb && item.Path != "" {
			if img := xfilepicker.GetThumbnailManager().LoadMemoryOnly(item.Path); item.MediaType != "audio" && img != nil {
				r.pendingThumbPath = ""
				r.applyThumbnail(item.Path, img)
			} else if r.pendingThumbPath != item.Path {
				r.thumbnailRequestID++
				requestID := r.thumbnailRequestID
				r.pendingThumbPath = item.Path
				path := item.Path
				apply := func(img *canvas.Image) {
					fyne.Do(func() {
						if r.currentPath != path || r.pendingThumbPath != path || r.thumbnailRequestID != requestID {
							return
						}
						r.pendingThumbPath = ""
						r.applyThumbnail(path, img)
					})
				}
				if item.MediaType == "audio" {
					go func() {
						apply(r.screen.queueAudioThumbnail(path))
					}()
				} else {
					uri := storage.NewFileURI(item.Path)
					go xfilepicker.GetThumbnailManager().Load(uri, apply)
				}
			}
		}
	} else {
		r.pendingThumbPath = ""
		r.thumbnail.Show()
		r.fallbackIcon.Hide()
	}

	if isCurrent {
		r.currentIcon.SetResource(theme.MediaPlayIcon())
		r.currentIcon.Show()
	} else {
		r.currentIcon.SetResource(nil)
		r.currentIcon.Hide()
	}

	r.Refresh()
}

func (r *queueRow) applyThumbnail(path string, img *canvas.Image) {
	if img == nil || r.currentPath != path {
		return
	}
	r.thumbnail.File = ""
	r.thumbnail.Resource = nil
	r.thumbnail.Image = img.Image
	r.thumbPath = path
	r.updateThumbnailFillMode()
	r.thumbnail.Refresh()
	r.thumbnail.Show()
	r.fallbackIcon.Hide()
}

func (r *queueRow) updateThumbnailFillMode() {
	if r.thumbnail == nil || r.thumbnail.Image == nil {
		return
	}

	height := r.content.Size().Height
	if height <= 0 {
		height = queueRowThumbHeight
	}

	targetAspect := queueRowThumbWidth / height
	if targetAspect <= 0 {
		targetAspect = queueRowThumbWidth / queueRowThumbHeight
	}

	fillMode := canvas.ImageFillContain
	if r.thumbnail.Aspect() >= targetAspect {
		fillMode = canvas.ImageFillCover
	}

	if r.thumbnail.FillMode == fillMode {
		return
	}

	r.thumbnail.FillMode = fillMode
	r.thumbnail.Refresh()
}

func (r *queueRowRenderer) Destroy() {}

func (r *queueRowRenderer) Layout(size fyne.Size) {
	r.row.content.Resize(size)
	r.row.updateThumbnailFillMode()
}

func (r *queueRowRenderer) MinSize() fyne.Size {
	return r.row.content.MinSize()
}

func (r *queueRowRenderer) Objects() []fyne.CanvasObject {
	return r.objects
}

func (r *queueRowRenderer) Refresh() {
	r.row.updateThumbnailFillMode()
	canvas.Refresh(r.row.content)
}

func (queueThumbLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}

	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(size)
}

func (queueThumbLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(queueRowThumbWidth, queueRowThumbHeight)
}
