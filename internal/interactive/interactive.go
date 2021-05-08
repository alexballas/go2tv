package interactive

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/alexballas/go2tv/internal/soapcalls"
	"github.com/gdamore/tcell/v2"
	"github.com/gdamore/tcell/v2/encoding"
	"github.com/mattn/go-runewidth"
)

// NewScreen .
type NewScreen struct {
	Current    tcell.Screen
	videoTitle string
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
	titleLen := len("Title: " + p.videoTitle)
	w, h := s.Size()
	boldStyle := tcell.StyleDefault.
		Background(tcell.ColorBlack).
		Foreground(tcell.ColorWhite).Bold(true)
	blinkStyle := tcell.StyleDefault.
		Background(tcell.ColorBlack).
		Foreground(tcell.ColorWhite).Blink(true)

	s.Clear()

	p.emitStr(w/2-titleLen/2, h/2-2, tcell.StyleDefault, "Title: "+p.videoTitle)
	if inputtext == "Waiting for status..." {
		p.emitStr(w/2-len(inputtext)/2, h/2, blinkStyle, inputtext)
	} else {
		p.emitStr(w/2-len(inputtext)/2, h/2, boldStyle, inputtext)
	}
	p.emitStr(1, 1, tcell.StyleDefault, "Press ESC to stop and exit.")
	p.emitStr(w/2-len("Press p to Pause/Play.")/2, h/2+2, tcell.StyleDefault, "Press p to Pause/Play.")

	s.Show()
}

// InterInit - Start the interactive terminal
func (p *NewScreen) InterInit(tv soapcalls.TVPayload) {
	var videoTitle string

	videoTitlefromURL, err := url.Parse(tv.VideoURL)
	if err != nil {
		videoTitle = tv.VideoURL
	} else {
		videoTitle = strings.TrimLeft(videoTitlefromURL.Path, "/")
	}
	p.videoTitle = videoTitle

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
			if ev.Key() == tcell.KeyEscape {
				tv.SendtoTV("Stop")
				s.Fini()
				os.Exit(0)
			} else if ev.Rune() == 'p' {
				if flipflop {
					flipflop = false
					tv.SendtoTV("Pause")
				} else {
					flipflop = true
					tv.SendtoTV("Play")
				}
			}
		}
	}
}

// Fini Method to implement the screen interface
func (p *NewScreen) Fini() {
	p.Current.Fini()
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
