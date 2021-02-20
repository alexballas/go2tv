package messages

import (
	"fmt"
	"reflect"

	"github.com/alexballas/go2tv/interactive"
)

// Emmiter - Define emmiter target
type Emmiter struct {
	Interactive bool
	Screen      *interactive.NewScreen
}

// EmmitMsg - Print message
func (e *Emmiter) EmmitMsg(msg string) {
	st := reflect.TypeOf(e.Screen)
	_, ok := st.MethodByName("DisplayAtext")
	if !ok {
		return
	}
	if e.Interactive {
		e.Screen.DisplayAtext(msg)
	} else {
		fmt.Println(msg)
	}
}
