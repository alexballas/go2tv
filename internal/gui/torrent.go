package gui

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/container"
	"github.com/alexballas/refyne/v2/dialog"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/alexballas/refyne/v2/storage"
	"github.com/alexballas/refyne/v2/theme"
	"github.com/alexballas/refyne/v2/widget"
	xfilepicker "github.com/alexballas/xfilepicker/dialog"

	"go2tv.app/go2tv/v2/internal/torrentstream"
)

type torrentUIState struct {
	operationMu   sync.Mutex
	mu            sync.Mutex
	session       *torrentstream.Session
	cancel        context.CancelFunc
	pendingCancel context.CancelFunc
	ctx           context.Context
	status        *widget.Label
	cancelButton  *widget.Button
	path          string
	dialog        dialog.Dialog
}

func fileSourceName(path string) string { return filepath.Base(path) }

func newTorrentButton(s *FyneScreen) *widget.Button {
	return widget.NewButtonWithIcon(lang.L("Torrent…"), theme.DownloadIcon(), func() { showTorrentDialog(s) })
}

func newTorrentStatus(s *FyneScreen) *widget.Label {
	label := widget.NewLabel("")
	label.Wrapping = fyne.TextWrapWord
	label.Hide()
	s.torrent.status = label
	return label
}

func newTorrentControls(s *FyneScreen) *fyne.Container {
	status := newTorrentStatus(s)
	button := widget.NewButton(lang.L("Cancel download"), func() { cancelTorrent(s) })
	button.Hide()
	s.torrent.cancelButton = button
	return container.NewBorder(nil, nil, nil, button, status)
}

func cancelTorrent(s *FyneScreen) {
	s.torrent.mu.Lock()
	session, path := s.torrent.session, s.torrent.path
	s.torrent.mu.Unlock()
	go func() {
		s.torrent.operationMu.Lock()
		defer s.torrent.operationMu.Unlock()
		s.torrent.mu.Lock()
		current := session != nil && s.torrent.session == session
		s.torrent.mu.Unlock()
		if !current {
			return
		}
		stopActionSync(s)
		s.closeTorrent()
		fyne.Do(func() { clearTorrentSelection(s, path) })
	}()
}

func (s *FyneScreen) closeTorrent() {
	s.torrent.mu.Lock()
	session, cancel := s.torrent.session, s.torrent.cancel
	s.torrent.session, s.torrent.cancel = nil, nil
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
	// Casting must release readers before removing the preceding cache.
	stopActionSync(s)
	s.closeTorrent()
	path, err := session.Select(index)
	if err != nil {
		return err
	}
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

func showTorrentDocument(s *FyneScreen, reader io.ReadCloser) {
	showTorrentInputDialog(s, reader)
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
	var labels []string
	var committed bool
	var d dialog.Dialog
	loadMagnet := widget.NewButton(lang.L("Load magnet"), nil)
	openFile := widget.NewButton(lang.L("Open .torrent"), nil)
	load := func(input string, reader io.ReadCloser) {
		cacheDir, err := torrentCacheDir()
		if err != nil {
			if reader != nil {
				_ = reader.Close()
			}
			status.SetText(err.Error())
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
		status.SetText(lang.L("Fetching torrent metadata…"))
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
					status.SetText(err.Error())
					return
				}
				pending, choices = session, found
				labels = make([]string, len(found))
				for i, file := range found {
					labels[i] = fmt.Sprintf("%s (%.1f MiB)", file.Name, float64(file.Size)/(1<<20))
				}
				files.SetOptions(labels)
				files.Enable()
				files.SetSelectedIndex(0)
				choose.Enable()
				status.SetText(lang.L("Choose a file, then cast. Missing pieces buffer automatically."))
			})
		}()
	}
	loadMagnet.OnTapped = func() {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(entry.Text)), "magnet:") {
			status.SetText(lang.L("Enter a magnet link."))
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
				status.SetText(err.Error())
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
	d = dialog.NewCustom(lang.L("Torrent"), lang.L("Cancel"), container.NewVBox(entry, container.NewGridWithColumns(2, loadMagnet, openFile), status, files, choose), s.Current)
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
	d.Resize(fyne.NewSize(520, 300))
	d.Show()
	if initialReader != nil {
		load("", initialReader)
	} else if len(initial) > 0 {
		load(initial[0], nil)
	}
}
