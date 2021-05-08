package screeninterfaces

// Screen interface
type Screen interface {
	EmitMsg(string)
	Fini()
}

// Emit .
func Emit(scr Screen, s string) {
	scr.EmitMsg(s)
}

// Close .
func Close(scr Screen) {
	scr.Fini()
}
