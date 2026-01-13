package interactive

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/alexballas/go2tv/castprotocol"
	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
)

// ChromecastScreen handles interactive CLI for Chromecast devices.
type ChromecastScreen struct {
	Current     tcell.Screen
	Client      *castprotocol.CastClient
	exitCTXfunc context.CancelFunc
	mediaTitle  string
	lastAction  string
	mu          sync.RWMutex
}

func (p *ChromecastScreen) emitStr(x, y int, style tcell.Style, str string) {
	s := p.Current
	for _, c := range str {
		var comb []rune
		w := runewidth.RuneWidth(c)
		if w == 0 {
			comb = []rune{c}
			c = ' '
			w = 1
		}
		s.SetContent(x, y, c, comb, style)
		x += w
	}
}

// EmitMsg displays status to the interactive terminal.
func (p *ChromecastScreen) EmitMsg(inputtext string) {
	p.updateLastAction(inputtext)
	s := p.Current

	p.mu.RLock()
	mediaTitle := p.mediaTitle
	p.mu.RUnlock()

	titleLen := len("Title: " + mediaTitle)
	w, h := s.Size()
	boldStyle := tcell.StyleDefault.
		Background(tcell.ColorBlack).
		Foreground(tcell.ColorWhite).Bold(true)
	blinkStyle := tcell.StyleDefault.
		Background(tcell.ColorBlack).
		Foreground(tcell.ColorWhite).Blink(true)

	s.Clear()

	p.emitStr(w/2-titleLen/2, h/2-2, tcell.StyleDefault, "Title: "+mediaTitle)
	switch inputtext {
	case "Waiting for status...", "Buffering...":
		p.emitStr(w/2-len(inputtext)/2, h/2, blinkStyle, inputtext)
	default:
		p.emitStr(w/2-len(inputtext)/2, h/2, boldStyle, inputtext)
	}
	p.emitStr(1, 1, tcell.StyleDefault, "Press ESC to stop and exit.")

	// Check mute status
	isMuted := false
	if p.Client != nil {
		status, err := p.Client.GetStatus()
		if err == nil {
			isMuted = status.Muted
		}
	}

	if isMuted {
		p.emitStr(w/2-len("MUTED")/2, h/2+2, blinkStyle, "MUTED")
	}

	p.emitStr(w/2-len(`"p" (Play/Pause)`)/2, h/2+4, tcell.StyleDefault, `"p" (Play/Pause)`)
	p.emitStr(w/2-len(`"m" (Mute/Unmute)`)/2, h/2+6, tcell.StyleDefault, `"m" (Mute/Unmute)`)
	p.emitStr(w/2-len(`"Page Up" "Page Down" (Volume Up/Down)`)/2, h/2+8, tcell.StyleDefault, `"Page Up" "Page Down" (Volume Up/Down)`)
	s.Show()
}

// InterInit starts the interactive terminal for Chromecast.
func (p *ChromecastScreen) InterInit(mediaPath string, c chan error) {
	p.mu.Lock()
	p.mediaTitle = filepath.Base(mediaPath)
	p.mu.Unlock()

	s := p.Current
	if err := s.Init(); err != nil {
		c <- fmt.Errorf("chromecast interactive: %w", err)
		return
	}

	defStyle := tcell.StyleDefault.
		Background(tcell.ColorBlack).
		Foreground(tcell.ColorWhite)
	s.SetStyle(defStyle)

	p.updateLastAction("Waiting for status...")
	p.EmitMsg(p.getLastAction())

	// Status polling goroutine
	statusTicker := time.NewTicker(1 * time.Second)
	var mediaStarted bool
	go func() {
		for range statusTicker.C {
			if p.Client == nil {
				continue
			}
			status, err := p.Client.GetStatus()
			if err != nil {
				continue
			}
			switch status.PlayerState {
			case "PLAYING":
				mediaStarted = true
				p.EmitMsg("Playing")
				// Detect media completion: Chromecast stops updating ~1-2s before end
				if status.Duration > 0 && status.CurrentTime >= status.Duration-1.5 {
					p.EmitMsg("Stopped")
					p.Fini()
				}
			case "PAUSED":
				mediaStarted = true
				p.EmitMsg("Paused")
			case "BUFFERING":
				mediaStarted = true
				p.EmitMsg("Buffering...")
			case "IDLE":
				if mediaStarted {
					p.EmitMsg("Stopped")
				}
			default:
				p.EmitMsg(p.getLastAction())
			}
		}
	}()

	for {
		switch ev := s.PollEvent().(type) {
		case *tcell.EventResize:
			s.Sync()
			p.EmitMsg(p.getLastAction())
		case *tcell.EventKey:
			p.HandleKeyEvent(ev)
		}
	}
}

// HandleKeyEvent handles key press events for Chromecast.
func (p *ChromecastScreen) HandleKeyEvent(ev *tcell.EventKey) {
	if p.Client == nil {
		return
	}

	if ev.Key() == tcell.KeyEscape {
		_ = p.Client.Stop()
		p.Fini()
	}

	if ev.Key() == tcell.KeyPgUp || ev.Key() == tcell.KeyPgDn {
		status, err := p.Client.GetStatus()
		if err != nil {
			return
		}

		delta := float32(0.05) // 5% volume change
		if ev.Key() == tcell.KeyPgDn {
			delta = -delta
		}

		newVolume := status.Volume + delta
		switch {
		case newVolume > 1.0:
			newVolume = 1.0
		case newVolume < 0.0:
			newVolume = 0.0
		}

		_ = p.Client.SetVolume(newVolume)
	}

	switch ev.Rune() {
	case 'p':
		status, err := p.Client.GetStatus()
		if err != nil {
			return
		}
		switch status.PlayerState {
		case "PLAYING":
			_ = p.Client.Pause()
		case "PAUSED":
			_ = p.Client.Play()
		}
	case 'm':
		status, err := p.Client.GetStatus()
		if err != nil {
			return
		}
		_ = p.Client.SetMuted(!status.Muted)
		p.EmitMsg(p.getLastAction())
	}
}

// Fini closes the screen and exits.
func (p *ChromecastScreen) Fini() {
	p.Current.Fini()
	p.exitCTXfunc()
}

// InitChromecastScreen creates a new Chromecast interactive screen.
func InitChromecastScreen(ctxCancel context.CancelFunc) (*ChromecastScreen, error) {
	s, err := tcell.NewScreen()
	if err != nil {
		return nil, fmt.Errorf("chromecast interactive: %w", err)
	}

	return &ChromecastScreen{
		Current:     s,
		exitCTXfunc: ctxCancel,
	}, nil
}

func (p *ChromecastScreen) getLastAction() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastAction
}

func (p *ChromecastScreen) updateLastAction(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastAction = s
}
