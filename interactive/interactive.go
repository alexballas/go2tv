package interactive

import (
	"errors"
	"fmt"
	"os"

	"github.com/alexballas/go2tv/soapcalls"
	"github.com/gdamore/tcell/v2"
	"github.com/gdamore/tcell/v2/encoding"
	"github.com/mattn/go-runewidth"
)

// NewScreen .
type NewScreen struct {
	Screen tcell.Screen
}

func emitStr(s tcell.Screen, x, y int, style tcell.Style, str string) {
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

func displayFirstText(s tcell.Screen) {
	w, h := s.Size()
	s.Clear()
	emitStr(s, w/2-10, h/2, tcell.StyleDefault, "Waiting for status...")
	emitStr(s, 1, 1, tcell.StyleDefault, "Press ESC to exit.")
	emitStr(s, w/2-10, h/2+2, tcell.StyleDefault, "Press P to Pause/Play.")
	emitStr(s, w/2-10, h/2+3, tcell.StyleDefault, "Press S to Stop.")
	s.Show()
}

func DisplayAtext(s tcell.Screen, inputtext string) {
	w, h := s.Size()
	s.Clear()
	emitStr(s, w/2-4, h/2, tcell.StyleDefault, inputtext)
	emitStr(s, 1, 1, tcell.StyleDefault, "Press ESC to exit.")
	emitStr(s, w/2-10, h/2+2, tcell.StyleDefault, "Press P to Pause/Play.")
	emitStr(s, w/2-10, h/2+3, tcell.StyleDefault, "Press S to Stop.")

	s.Show()
}

// InterInit - Start the interactive terminal
func (p *NewScreen) InterInit(tv soapcalls.TVPayload) {
	encoding.Register()
	s := p.Screen
	if e := s.Init(); e != nil {
		fmt.Fprintf(os.Stderr, "%v\n", e)
		os.Exit(1)
	}

	defStyle := tcell.StyleDefault.
		Background(tcell.ColorBlack).
		Foreground(tcell.ColorWhite)
	s.SetStyle(defStyle)

	displayFirstText(s)

	for {
		switch ev := s.PollEvent().(type) {
		case *tcell.EventResize:
			s.Sync()
			displayFirstText(s)
		case *tcell.EventKey:
			if ev.Key() == tcell.KeyEscape {
				tv.SendtoTV("Stop")
				s.Fini()
				os.Exit(0)
			} else if ev.Rune() == 'p' {
				DisplayAtext(s, "Playing")
				tv.SendtoTV("Pause")
			} else if ev.Rune() == 's' {
				DisplayAtext(s, "Stopped")
			}
		}
	}
}

// InitNewScreen .
func InitNewScreen() (NewScreen, error) {
	s, e := tcell.NewScreen()
	if e != nil {
		return NewScreen{}, errors.New("Can't start new interactive screen")
	}
	q := NewScreen{
		Screen: s,
	}
	return q, nil
}
