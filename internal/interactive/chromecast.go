package interactive

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"go2tv.app/go2tv/v2/castprotocol"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/utils"
)

// ChromecastScreen handles interactive CLI for Chromecast devices.
type ChromecastScreen struct {
	Current     tcell.Screen
	Client      *castprotocol.CastClient
	exitCTXfunc context.CancelFunc
	mediaTitle  string
	lastAction  string
	// AudioLabel names a non-default audio track chosen with -a.
	AudioLabel string
	// Duration is the source length of a transcoded stream; the receiver
	// only sees the stream from the last restart offset.
	Duration float64
	// Restart reloads a transcoded stream at a source position. Nil means
	// the receiver seeks natively.
	Restart func(seconds int) error
	offset  int
	skips   *skipper
	note    notice
	idle    idleWatch
	mu      sync.RWMutex
}

// reloadTimeout bounds how long IDLE is ignored after a LOAD was sent but
// the receiver never started a new media session (e.g. it rejected it).
const reloadTimeout = 20 * time.Second

// idleWatch decides when an IDLE receiver means playback is over. The 1s poll
// can miss the last seconds before IDLE. A LOAD (initial cast or transcoded
// skip) passes through IDLE without ending playback, and the receiver keeps
// reporting the replaced session until it handles the LOAD.
type idleWatch struct {
	mu       sync.Mutex
	started  bool
	loading  bool      // LOAD pending: ignore IDLE and the replaced session
	sent     bool      // LOAD sent; a new session ends loading
	session  int       // last session seen; replaced session while loading
	deadline time.Time // give up waiting for the new session
	now      func() time.Time
}

func (w *idleWatch) clock() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

func (w *idleWatch) beginLoad() {
	w.mu.Lock()
	w.loading, w.sent = true, false
	w.mu.Unlock()
}

// loadSent records whether LOAD reached the receiver; it acts on it later.
func (w *idleWatch) loadSent(ok bool) {
	w.mu.Lock()
	w.loading, w.sent = ok, ok
	w.deadline = w.clock().Add(reloadTimeout)
	w.mu.Unlock()
}

// ended reports whether a receiver state in media session ends playback.
func (w *idleWatch) ended(state string, session int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch state {
	case "PLAYING", "PAUSED", "BUFFERING":
		switch {
		case !w.loading:
			w.started, w.session = true, session
		case !w.sent:
			w.session = session // seen before LOAD went out: the replaced one
		case session != w.session || w.clock().After(w.deadline):
			// A new session, or the same ID after a receiver relaunch.
			w.loading, w.started, w.session = false, true, session
		}
	case "IDLE":
		if w.loading {
			return w.sent && w.started && w.clock().After(w.deadline)
		}
		return w.started
	}
	return false
}

// LoadStarted marks a LOAD about to be sent; call LoadSent with its result.
func (p *ChromecastScreen) LoadStarted() { p.idle.beginLoad() }

// LoadSent records whether the LOAD was sent.
func (p *ChromecastScreen) LoadSent(ok bool) { p.idle.loadSent(ok) }

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
	if p.AudioLabel != "" {
		audio := "Audio: " + p.AudioLabel
		p.emitStr(w/2-runewidth.StringWidth(audio)/2, h/2-1, tcell.StyleDefault, audio)
	}
	if note := p.note.get(); note != "" {
		p.emitStr(w/2-len(note)/2, h/2+1, tcell.StyleDefault, note)
	}
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
	p.emitStr(w/2-runewidth.StringWidth(skipHelp)/2, h/2+10, tcell.StyleDefault, skipHelp)
	s.Show()
}

// mediaEnded reports playback reaching the end; Chromecast stops updating
// ~1-2s early. Transcoded streams report the buffered length as duration, so
// the source duration wins when known.
func mediaEnded(current, receiverDuration float32, offset int, sourceDuration float64) bool {
	total := receiverDuration
	if sourceDuration > 0 {
		total = float32(sourceDuration)
	}
	return total > 0 && float32(offset)+current >= total-1.5
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
	statusTicker := time.NewTicker(time.Second)
	go func() {
		for range statusTicker.C {
			if p.Client == nil {
				continue
			}
			status, err := p.Client.GetStatus()
			if err != nil {
				continue
			}
			if p.idle.ended(status.PlayerState, status.MediaSessionID) {
				p.EmitMsg("Stopped")
				p.Fini()
				continue
			}
			switch status.PlayerState {
			case "PLAYING":
				p.EmitMsg("Playing")
				if mediaEnded(status.CurrentTime, status.Duration, p.streamOffset(), p.Duration) {
					p.EmitMsg("Stopped")
					p.Fini()
				}
			case "PAUSED":
				p.EmitMsg("Paused")
			case "BUFFERING":
				p.EmitMsg("Buffering...")
			case "IDLE":
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

	if delta, ok := skipDelta(ev.Key()); ok {
		p.queueSkip(delta)
		return
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

func (p *ChromecastScreen) streamOffset() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.offset
}

func (p *ChromecastScreen) queueSkip(delta int) {
	p.mu.Lock()
	if p.skips == nil {
		p.skips = &skipper{seek: p.skip}
	}
	skips := p.skips
	p.mu.Unlock()
	skips.add(delta)
	p.note.set(formatSkip(skips.pending()))
	p.EmitMsg(p.getLastAction())
}

func (p *ChromecastScreen) skip(delta int) {
	target, err := p.seekRelative(delta)
	if err != nil {
		p.note.set("Seek unavailable")
	} else {
		p.note.set("Seeked to " + utils.SecondsToClockTime(target))
	}
	p.EmitMsg(p.getLastAction())
}

func (p *ChromecastScreen) seekRelative(delta int) (int, error) {
	status, err := p.Client.GetStatus()
	if err != nil {
		return 0, fmt.Errorf("seekRelative: %w", err)
	}
	current := int(status.CurrentTime) + p.streamOffset()
	if p.Restart == nil {
		target := playback.SkipTarget(current, delta, int(status.Duration))
		if err := p.Client.Seek(target); err != nil {
			return 0, fmt.Errorf("seekRelative: %w", err)
		}
		return target, nil
	}
	if p.Duration <= 0 {
		return 0, errors.New("seekRelative: transcoded duration unknown")
	}
	target := playback.SkipTarget(current, delta, int(p.Duration))
	p.idle.beginLoad()
	if err := p.Restart(target); err != nil {
		p.idle.loadSent(false)
		return 0, fmt.Errorf("seekRelative: %w", err)
	}
	p.idle.loadSent(true)
	p.mu.Lock()
	p.offset = target
	p.mu.Unlock()
	return target, nil
}
