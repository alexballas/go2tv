package messages

import (
	"fmt"

	"github.com/alexballas/go2tv/interactive"
)

// Emmiter - Define emmiter target
type Emmiter struct {
	Interactive bool
	screen      interactive.NewScreen
}

// EmmitMsg - Print message
func (e *Emmiter) EmmitMsg(msg string) {
	if e.Interactive {
		//interactive.DisplayAtext("ss")
	} else {
		fmt.Println(msg)
	}
}
