package interactive

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"go2tv.app/go2tv/v2/internal/playback"
	"go2tv.app/go2tv/v2/soapcalls"
	"go2tv.app/go2tv/v2/utils"
)

// NewScreen .
type NewScreen struct {
	Current     tcell.Screen
	TV          *soapcalls.TVPayload
	exitCTXfunc context.CancelFunc
	mediaTitle  string
	lastAction  string
	// AudioLabel names a non-default audio track chosen with -a.
	AudioLabel string
	skips      *skipper
	note       notice
	mu         sync.RWMutex
}

func (p *NewScreen) emitStr(x, y int, style tcell.Style, str string) {
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

// EmitMsg displays the actions to the interactive terminal.
// Method to implement the screen interface
func (p *NewScreen) EmitMsg(inputtext string) {
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
	if inputtext == "Waiting for status..." {
		p.emitStr(w/2-len(inputtext)/2, h/2, blinkStyle, inputtext)
	} else {
		p.emitStr(w/2-len(inputtext)/2, h/2, boldStyle, inputtext)
	}
	if note := p.note.get(); note != "" {
		p.emitStr(w/2-len(note)/2, h/2+1, tcell.StyleDefault, note)
	}
	p.emitStr(1, 1, tcell.StyleDefault, "Press ESC to stop and exit.")

	isMute := "0"
	var err error

	if p.TV != nil {
		isMute, err = p.TV.GetMuteSoapCall()
	}

	if err != nil || isMute == "0" {
		p.emitStr(w/2-len("")/2, h/2+2, tcell.StyleDefault, "")
	} else {
		p.emitStr(w/2-len("MUTED")/2, h/2+2, blinkStyle, "MUTED")
	}
	p.emitStr(w/2-len(`"p" (Play/Pause)`)/2, h/2+4, tcell.StyleDefault, `"p" (Play/Pause)`)
	p.emitStr(w/2-len(`"m" (Mute/Unmute)`)/2, h/2+6, tcell.StyleDefault, `"m" (Mute/Unmute)`)
	p.emitStr(w/2-len(`"Page Up" "Page Down" (Volume Up/Down)`)/2, h/2+8, tcell.StyleDefault, `"Page Up" "Page Down" (Volume Up/Down)`)
	p.emitStr(w/2-runewidth.StringWidth(skipHelp)/2, h/2+10, tcell.StyleDefault, skipHelp)
	s.Show()
}

// InterInit starts the interactive terminal
func (p *NewScreen) InterInit(tv *soapcalls.TVPayload, c chan error) {
	p.TV = tv

	muteChecker := time.NewTicker(time.Second)

	go func() {
		for range muteChecker.C {
			p.EmitMsg(p.getLastAction())
		}
	}()

	p.mu.Lock()

	mediaTitlefromURL, err := url.Parse(tv.MediaURL)
	if err != nil {
		c <- fmt.Errorf("interactive screen error: %w", err)
		return
	}
	p.mediaTitle = strings.TrimLeft(mediaTitlefromURL.Path, "/")

	p.mu.Unlock()

	s := p.Current
	if err := s.Init(); err != nil {
		c <- fmt.Errorf("interactive screen error: %w", err)
		return
	}

	defStyle := tcell.StyleDefault.
		Background(tcell.ColorBlack).
		Foreground(tcell.ColorWhite)
	s.SetStyle(defStyle)

	p.updateLastAction("Waiting for status...")
	p.EmitMsg(p.getLastAction())

	// Sending the Play1 action sooner may result
	// in a panic error since we need to properly
	// initialize the tcell window.
	if err := tv.SendtoTV("Play1"); err != nil {
		c <- fmt.Errorf("interactive screen error: %w", err)
		return
	}

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

// HandleKeyEvent Method to handle all key press events
func (p *NewScreen) HandleKeyEvent(ev *tcell.EventKey) {
	tv := p.TV
	if tv == nil {
		return
	}

	if ev.Key() == tcell.KeyEscape {
		_ = tv.SendtoTV("Stop")
		p.Fini()
	}

	if delta, ok := skipDelta(ev.Key()); ok {
		p.queueSkip(delta)
		return
	}

	if ev.Key() == tcell.KeyPgUp || ev.Key() == tcell.KeyPgDn {
		currentVolume, err := tv.GetVolumeSoapCall()
		if err != nil {
			return
		}

		setVolume := currentVolume - 1
		if ev.Key() == tcell.KeyPgUp {
			setVolume = currentVolume + 1
		}

		stringVolume := strconv.Itoa(setVolume)

		if err := tv.SetVolumeSoapCall(stringVolume); err != nil {
			return
		}
	}

	switch ev.Rune() {
	case 'p':
		action, err := resolvePlayPauseAction(tv)
		if err != nil {
			break
		}
		_ = tv.SendtoTV(action)

	case 'm':
		currentMute, err := tv.GetMuteSoapCall()
		if err != nil {
			break
		}
		switch currentMute {
		case "1":
			if err = tv.SetMuteSoapCall("0"); err == nil {
				p.EmitMsg(p.getLastAction())
			}
		case "0":
			if err = tv.SetMuteSoapCall("1"); err == nil {
				p.EmitMsg(p.getLastAction())
			}
		}
	}
}

// Fini Method to implement the screen interface
func (p *NewScreen) Fini() {
	p.Current.Fini()
	p.exitCTXfunc()
}

// SetMediaType Method to implement the screen interface
func (p *NewScreen) SetMediaType(mediaType string) {
	// No-op for interactive mode
}

// InitTcellNewScreen .
func InitTcellNewScreen(ctxCancel context.CancelFunc) (*NewScreen, error) {
	s, err := tcell.NewScreen()
	if err != nil {
		return nil, fmt.Errorf("interactive screen error: %w", err)
	}

	return &NewScreen{
		Current:     s,
		exitCTXfunc: ctxCancel,
	}, nil
}

func (p *NewScreen) getLastAction() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastAction
}

func (p *NewScreen) updateLastAction(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastAction = s
}

func resolvePlayPauseAction(tv *soapcalls.TVPayload) (string, error) {
	transportInfo, err := tv.GetTransportInfo()
	if err != nil {
		return "", err
	}

	if len(transportInfo) == 0 {
		return "Play", nil
	}

	return playPauseActionFromState(transportInfo[0]), nil
}

func playPauseActionFromState(state string) string {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "PLAYING":
		return "Pause"
	case "PAUSED_PLAYBACK", "PAUSED_RECORDING", "PAUSED":
		return "Play"
	default:
		return "Play"
	}
}

func (p *NewScreen) queueSkip(delta int) {
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

func (p *NewScreen) skip(delta int) {
	target, err := dlnaSkip(p.TV, delta)
	if err != nil {
		p.note.set("Seek unavailable")
	} else {
		p.note.set("Seeked to " + utils.SecondsToClockTime(target))
	}
	p.EmitMsg(p.getLastAction())
}

// dlnaSkip seeks relative to the renderer position. Transcoded streams
// restart FFmpeg at the target, since their timeline starts at the offset.
func dlnaSkip(tv *soapcalls.TVPayload, delta int) (int, error) {
	if tv == nil {
		return 0, errors.New("dlnaSkip: no renderer")
	}
	position, err := tv.GetPositionInfo()
	if err != nil {
		return 0, fmt.Errorf("dlnaSkip: %w", err)
	}
	if len(position) < 2 {
		return 0, errors.New("dlnaSkip: incomplete position")
	}
	current, err := utils.ClockTimeToSeconds(position[1])
	if err != nil {
		return 0, fmt.Errorf("dlnaSkip: %w", err)
	}
	if tv.Transcode {
		total := int(math.Round(tv.MediaDuration))
		if total <= 0 {
			return 0, errors.New("dlnaSkip: transcoded duration unknown")
		}
		target := playback.SkipTarget(current+tv.FFmpegSeek, delta, total)
		tv.SetTranscodeSeek(target, tv.FFmpegSubsPath, tv.FFmpegEmbeddedSubtitle)
		_ = tv.SendtoTV("Stop")
		if err := tv.SendtoTV("Play1"); err != nil {
			return 0, fmt.Errorf("dlnaSkip: %w", err)
		}
		return target, nil
	}
	if !tv.Seekable {
		return 0, errors.New("dlnaSkip: media not seekable")
	}
	total, _ := utils.ClockTimeToSeconds(position[0])
	target := playback.SkipTarget(current, delta, total)
	if err := tv.SeekSoapCall(utils.SecondsToClockTime(target)); err != nil {
		return 0, fmt.Errorf("dlnaSkip: %w", err)
	}
	return target, nil
}
