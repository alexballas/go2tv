package screeninterfaces

type Screen interface {
	EmitMsg(string)
	Fini()
}

func Emit(scr Screen, s string) {
	scr.EmitMsg(s)
}
func Close(scr Screen) {
	scr.Fini()
}
