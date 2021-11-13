package interactive

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/alexballas/go2tv/internal/soapcalls"
	"github.com/alexballas/go2tv/internal/urldownloader"
	"github.com/gdamore/tcell/v2"
	"github.com/gdamore/tcell/v2/encoding"
	"github.com/mattn/go-runewidth"
)

// NewScreen .
type NewScreen struct {
	Current    tcell.Screen
	TV         *soapcalls.TVPayload
	mediaTitle string
	lastAction string
}

var flipflop bool = true

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

// EmitMsg - Display the actions to the interactive terminal.
// Method to implement the screen interface
func (p *NewScreen) EmitMsg(inputtext string) {
	p.lastAction = inputtext
	s := p.Current
	titleLen := len("Title: " + p.mediaTitle)
	w, h := s.Size()
	boldStyle := tcell.StyleDefault.
		Background(tcell.ColorBlack).
		Foreground(tcell.ColorWhite).Bold(true)
	blinkStyle := tcell.StyleDefault.
		Background(tcell.ColorBlack).
		Foreground(tcell.ColorWhite).Blink(true)

	s.Clear()

	p.emitStr(w/2-titleLen/2, h/2-2, tcell.StyleDefault, "Title: "+p.mediaTitle)
	if inputtext == "Waiting for status..." {
		p.emitStr(w/2-len(inputtext)/2, h/2, blinkStyle, inputtext)
	} else {
		p.emitStr(w/2-len(inputtext)/2, h/2, boldStyle, inputtext)
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
	p.emitStr(w/2-len(`Press "p" to Pause/Play, "m" to Mute/Unmute`)/2, h/2+4, tcell.StyleDefault, `Press "p" to Pause/Play, "m" to Mute/Unmute`)

	s.Show()
}

// InterInit - Start the interactive terminal
func (p *NewScreen) InterInit(tv *soapcalls.TVPayload) {
	p.TV = tv

	muteChecker := time.NewTicker(1 * time.Second)

	go func() {
		for range muteChecker.C {
			p.EmitMsg(p.lastAction)
		}
	}()

	p.mediaTitle = tv.MediaURL
	mediaTitlefromURL, err := url.Parse(tv.MediaURL)
	if err == nil {
		p.mediaTitle = strings.TrimLeft(mediaTitlefromURL.Path, "/")
	}

	encoding.Register()
	s := p.Current
	if e := s.Init(); e != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", e)
		os.Exit(1)
	}

	defStyle := tcell.StyleDefault.
		Background(tcell.ColorBlack).
		Foreground(tcell.ColorWhite)
	s.SetStyle(defStyle)

	p.lastAction = "Waiting for status..."
	p.EmitMsg(p.lastAction)

	for {
		switch ev := s.PollEvent().(type) {
		case *tcell.EventResize:
			s.Sync()
			p.EmitMsg(p.lastAction)
		case *tcell.EventKey:
			p.HandleKeyEvent(ev)
		}
	}
}

// HandleKeyEvent Method to handle all key press events
func (p *NewScreen) HandleKeyEvent(ev *tcell.EventKey) {
	tv := p.TV

	if ev.Key() == tcell.KeyEscape {
		tv.SendtoTV("Stop")
		p.Fini()
	}

	switch ev.Rune() {
	case 'p':
		if flipflop {
			flipflop = false
			tv.SendtoTV("Pause")
		} else {
			flipflop = true
			tv.SendtoTV("Play")
		}
	case 'm':
		currentMute, err := tv.GetMuteSoapCall()
		if err != nil {
			break
		}
		switch currentMute {
		case "1":
			if err = tv.SetMuteSoapCall("0"); err == nil {
				p.EmitMsg(p.lastAction)
			}
		case "0":
			if err = tv.SetMuteSoapCall("1"); err == nil {
				p.EmitMsg(p.lastAction)
			}
		}
	}
}

// Fini Method to implement the screen interface
func (p *NewScreen) Fini() {
	p.Current.Fini()

	switch t := p.TV.MediaFile.(type) {
	case *urldownloader.TFile:
		t.Close()
	}

	os.Exit(0)
}

// InitTcellNewScreen .
func InitTcellNewScreen() (*NewScreen, error) {
	s, e := tcell.NewScreen()
	if e != nil {
		return nil, errors.New("can't start new interactive screen")
	}
	return &NewScreen{
		Current: s,
	}, nil
}
