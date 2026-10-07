package gui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/widget"
	xfilepicker "github.com/alexballas/xfilepicker/dialog"

	"go2tv.app/go2tv/v2/internal/torrentstream"
)

type torrentUIState struct {
	operationMu   sync.Mutex
	mu            sync.Mutex
	session       *torrentstream.Session
	cancel        context.CancelFunc
	cancelDone    chan struct{}
	pendingCancel context.CancelFunc
	startup       *playbackStartup
	playback      *torrentstream.Session
	ctx           context.Context
	status        *widget.Label
	cancelButton  *widget.Button
	path          string
	dialog        dialog.Dialog
}

func (s *FyneScreen) hasTorrentSession() bool {
	s.torrent.mu.Lock()
	defer s.torrent.mu.Unlock()
	return s.torrent.session != nil
}

func newTorrentControls(s *FyneScreen) *fyne.Container {
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	status.Hide()
	s.torrent.status = status
	button := widget.NewButton(lang.L("Cancel download"), func() { cancelTorrent(s) })
	button.Hide()
	s.torrent.cancelButton = button
	return container.NewBorder(nil, nil, nil, button, status)
}

func cancelTorrent(s *FyneScreen) {
	s.torrent.mu.Lock()
	session, path := s.torrent.session, s.torrent.path
	previous := s.torrent.cancelDone
	done := make(chan struct{})
	s.torrent.cancelDone = done
	// Signal the running worker before queued teardown can race with startup.
	startup := s.torrent.startup
	if session != nil && startup != nil && startup.session == session {
		startup.cancel()
	} else {
		startup = nil
	}
	s.torrent.mu.Unlock()
	if startup != nil {
		s.nextChromecastActionID()
	}
	go func() {
		defer s.finishTorrentCancellation(done)
		// Preserve request order so waiting for the latest cancellation also
		// waits for every preceding teardown.
		if previous != nil {
			<-previous
		}
		if startup != nil {
			<-startup.done
		}
		s.torrent.operationMu.Lock()
		defer s.torrent.operationMu.Unlock()
		s.torrent.mu.Lock()
		current := session != nil && s.torrent.session == session
		s.torrent.mu.Unlock()
		if !current {
			return
		}
		if s.torrentOwnsPlayback(session, path) {
			stopActionSync(s)
		}
		s.closeTorrent()
		// Include UI cleanup (including closeTorrent's queued service sync)
		// before publishing cancellation completion.
		fyne.DoAndWait(func() { clearTorrentSelection(s, path) })
	}()
}

func (s *FyneScreen) finishTorrentCancellation(done chan struct{}) {
	s.torrent.mu.Lock()
	if s.torrent.cancelDone == done {
		s.torrent.cancelDone = nil
	}
	s.torrent.mu.Unlock()
	refreshPlaybackControls("", s)
	fyne.DoAndWait(func() {})
	close(done)
}

func (s *FyneScreen) torrentCancellationDone() <-chan struct{} {
	s.torrent.mu.Lock()
	defer s.torrent.mu.Unlock()
	return s.torrent.cancelDone
}

func (s *FyneScreen) closeTorrent() {
	if done := s.cancelTorrentStartup(); done != nil {
		<-done
	}
	s.torrent.mu.Lock()
	session, cancel := s.torrent.session, s.torrent.cancel
	s.torrent.session, s.torrent.cancel = nil, nil
	if s.torrent.playback == session {
		s.torrent.playback = nil
	}
	s.torrent.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if session != nil {
		_ = session.Close()
	}
	fyne.Do(func() {
		if s.torrent.status != nil {
			s.torrent.status.Hide()
		}
		if s.torrent.cancelButton != nil {
			s.torrent.cancelButton.Hide()
		}
		syncTorrentBackgroundSession(s)
	})
}

func (s *FyneScreen) shutdownTorrents() {
	s.torrent.operationMu.Lock()
	defer s.torrent.operationMu.Unlock()
	s.torrent.mu.Lock()
	cancel := s.torrent.pendingCancel
	s.torrent.pendingCancel = nil
	s.torrent.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.closeTorrent()
}

func (s *FyneScreen) useTorrent(session *torrentstream.Session, cancel context.CancelFunc, index int) error {
	s.torrent.operationMu.Lock()
	defer s.torrent.operationMu.Unlock()
	path, err := session.Select(index)
	if err != nil {
		return err
	}
	// Validate the new selection before interrupting existing playback.
	// Casting must release readers before removing the preceding cache.
	stopActionSync(s)
	s.closeTorrent()
	s.torrent.mu.Lock()
	s.torrent.session, s.torrent.cancel = session, cancel
	s.torrent.path = path
	s.torrent.mu.Unlock()
	selectTorrentMedia(s, path)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			s.torrent.mu.Lock()
			current := s.torrent.session == session
			s.torrent.mu.Unlock()
			if !current {
				return
			}
			completed, total := session.Progress()
			fyne.Do(func() {
				s.torrent.mu.Lock()
				defer s.torrent.mu.Unlock()
				if s.torrent.session != session || s.torrent.status == nil {
					return
				}
				text := lang.L("Torrent: waiting for peers")
				if total > 0 && completed > 0 {
					text = fmt.Sprintf("%s: %.1f%% · %.1f / %.1f MiB", lang.L("Torrent"), 100*float64(completed)/float64(total), float64(completed)/(1<<20), float64(total)/(1<<20))
				}
				s.torrent.status.SetText(text)
				s.torrent.status.Show()
				if s.torrent.cancelButton != nil {
					s.torrent.cancelButton.Show()
				}
			})
			<-ticker.C
		}
	}()
	return nil
}

// Loading and choosing are separate: metadata never starts a full download.
func showTorrentDialog(s *FyneScreen, initial ...string) {
	showTorrentInputDialog(s, nil, initial...)
}

func showTorrentInputDialog(s *FyneScreen, initialReader io.ReadCloser, initial ...string) {
	if s.torrent.dialog != nil {
		if initialReader != nil {
			_ = initialReader.Close()
		}
		s.torrent.dialog.Show()
		return
	}
	entry := widget.NewEntry()
	entry.SetPlaceHolder(lang.L("Paste magnet link"))
	status := widget.NewLabel(lang.L("Paste a magnet link or open a .torrent file."))
	status.Wrapping = fyne.TextWrapWord
	files := widget.NewSelect(nil, nil)
	files.PlaceHolder = lang.L("Choose media file")
	files.Disable()
	choose := widget.NewButton(lang.L("Use file"), nil)
	choose.Disable()
	var pending *torrentstream.Session
	var cancel context.CancelFunc
	var choices []torrentstream.File
	var committed bool
	var d dialog.Dialog
	var content *container.Scroll
	setStatus := func(text string) {
		status.SetText(text)
		if content != nil {
			content.Content.Refresh()
			resizeTorrentDialog(s, d, content)
		}
	}
	loadMagnet := widget.NewButton(lang.L("Load magnet"), nil)
	openFile := widget.NewButton(lang.L("Open .torrent"), nil)
	load := func(input string, reader io.ReadCloser) {
		cacheDir, err := torrentCacheDir()
		if err != nil {
			if reader != nil {
				_ = reader.Close()
			}
			setStatus(err.Error())
			return
		}
		if cancel != nil {
			cancel()
		}
		if pending != nil {
			old := pending
			go old.Close()
			pending = nil
		}
		parent := s.torrent.ctx
		if parent == nil {
			parent = context.Background()
		}
		ctx, stop := context.WithCancel(parent)
		cancel = stop
		s.torrent.mu.Lock()
		s.torrent.pendingCancel = stop
		s.torrent.mu.Unlock()
		loadMagnet.Disable()
		openFile.Disable()
		choose.Disable()
		files.Disable()
		setStatus(lang.L("Fetching torrent metadata…"))
		go func() {
			var session *torrentstream.Session
			var err error
			if reader != nil {
				stopClose := context.AfterFunc(ctx, func() { _ = reader.Close() })
				defer stopClose()
				defer reader.Close()
				session, err = torrentstream.OpenReaderWithOptions(ctx, reader, torrentstream.Options{CacheDir: cacheDir})
			} else {
				session, err = torrentstream.OpenWithOptions(ctx, input, torrentstream.Options{CacheDir: cacheDir})
			}
			var found []torrentstream.File
			if err == nil {
				found, err = session.Files(ctx)
			}
			if err != nil && session != nil {
				_ = session.Close()
			}
			fyne.Do(func() {
				if ctx.Err() != nil {
					if session != nil {
						go session.Close()
					}
					return
				}
				loadMagnet.Enable()
				openFile.Enable()
				if err != nil {
					setStatus(err.Error())
					return
				}
				pending, choices = session, found
				labels := make([]string, len(found))
				for i, file := range found {
					labels[i] = fmt.Sprintf("%s (%.1f MiB)", file.Name, float64(file.Size)/(1<<20))
				}
				files.SetOptions(labels)
				files.Enable()
				files.SetSelectedIndex(0)
				choose.Enable()
				setStatus(lang.L("Choose a file, then cast. Missing pieces buffer automatically."))
			})
		}()
	}
	loadMagnet.OnTapped = func() {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(entry.Text)), "magnet:") {
			setStatus(lang.L("Enter a magnet link."))
			return
		}
		load(entry.Text, nil)
	}
	openFile.OnTapped = func() {
		xfilepicker.SetFFmpegPath(s.ffmpegPath)
		resumeHotkeys := suspendHotkeys(s)
		picker := xfilepicker.NewFileOpen(func(readers []fyne.URIReadCloser, err error) {
			defer resumeHotkeys()
			if err != nil {
				setStatus(err.Error())
				return
			}
			if len(readers) > 0 {
				load("", readers[0])
			}
		}, s.Current, false)
		if f, ok := picker.(xfilepicker.FilePicker); ok {
			f.SetFilter(storage.NewExtensionFileFilter([]string{".torrent"}))
		}
		picker.Show()
	}
	choose.OnTapped = func() {
		if pending == nil {
			return
		}
		index := files.SelectedIndex()
		if index < 0 || index >= len(choices) {
			return
		}
		session, stop, fileIndex := pending, cancel, choices[index].Index
		committed = true
		d.Hide()
		go func() {
			if err := s.useTorrent(session, stop, fileIndex); err != nil {
				stop()
				_ = session.Close()
				fyne.Do(func() { dialog.ShowError(err, s.Current) })
			}
		}()
	}
	content = newTorrentDialogContent(entry, container.NewGridWithColumns(2, loadMagnet, openFile), status, files, choose)
	d = dialog.NewCustom(lang.L("Torrent"), lang.L("Cancel"), content, s.Current)
	s.torrent.dialog = d
	d.SetOnClosed(func() {
		s.torrent.dialog = nil
		if committed {
			return
		}
		if cancel != nil {
			cancel()
		}
		if pending != nil {
			go pending.Close()
		}
	})
	resizeTorrentDialog(s, d, content)
	d.Show()
	if initialReader != nil {
		load("", initialReader)
	} else if len(initial) > 0 {
		load(initial[0], nil)
	}
}
