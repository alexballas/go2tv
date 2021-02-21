package messages

import (
	"reflect"

	"github.com/alexballas/go2tv/interactive"
)

// Emmiter - Define emmiter target
type Emmiter struct {
	Screen *interactive.NewScreen
}

// EmmitMsg - Print message
func (e *Emmiter) EmmitMsg(msg string) {
	st := reflect.TypeOf(e.Screen)
	_, ok := st.MethodByName("DisplayAtext")
	if !ok {
		return
	}
	e.Screen.DisplayAtext(msg)
}
