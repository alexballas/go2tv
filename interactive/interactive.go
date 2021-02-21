package interactive

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/alexballas/go2tv/soapcalls"
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

func (p *NewScreen) displayFirstText() {
	s := p.Current
	titleLen := len("Title:")
	w, h := s.Size()
	s.Clear()
	p.emitStr(w/2-titleLen/2, h/2-2, tcell.StyleDefault, "Title:")
	p.emitStr(w/2-10, h/2, tcell.StyleDefault, "Waiting for status...")
	p.emitStr(1, 1, tcell.StyleDefault, "Press ESC / q to exit.")
	p.emitStr(w/2-10, h/2+2, tcell.StyleDefault, "Press p to Pause/Play.")
	p.emitStr(w/2-10, h/2+3, tcell.StyleDefault, "Press s to Stop.")
	s.Show()
}

//DisplayAtext .
func (p *NewScreen) DisplayAtext(inputtext string) {
	p.lastAction = inputtext
	s := p.Current
	titleLen := len("Title: " + p.videoTitle)
	w, h := s.Size()
	s.Clear()
	p.emitStr(w/2-titleLen/2, h/2-2, tcell.StyleDefault, "Title: "+p.videoTitle)
	p.emitStr(w/2-8, h/2, tcell.StyleDefault, inputtext)
	p.emitStr(1, 1, tcell.StyleDefault, "Press ESC / q to exit.")
	p.emitStr(w/2-10, h/2+2, tcell.StyleDefault, "Press p to Pause/Play.")
	p.emitStr(w/2-10, h/2+3, tcell.StyleDefault, "Press s to Stop.")

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
		fmt.Fprintf(os.Stderr, "%v\n", e)
		os.Exit(1)
	}

	defStyle := tcell.StyleDefault.
		Background(tcell.ColorBlack).
		Foreground(tcell.ColorWhite)
	s.SetStyle(defStyle)

	p.displayFirstText()
	p.lastAction = "Waiting for status..."
	for {
		switch ev := s.PollEvent().(type) {
		case *tcell.EventResize:
			s.Sync()
			p.DisplayAtext(p.lastAction)
		case *tcell.EventKey:
			if ev.Key() == tcell.KeyEscape ||
				ev.Rune() == 'q' {
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
			} else if ev.Rune() == 's' {
				tv.SendtoTV("Stop")
				s.Fini()
				os.Exit(0)
			}
		}
	}
}

// InitNewScreen .
func InitNewScreen() (*NewScreen, error) {
	s, e := tcell.NewScreen()
	if e != nil {
		return &NewScreen{}, errors.New("Can't start new interactive screen")
	}
	q := NewScreen{
		Current: s,
	}
	return &q, nil
}
