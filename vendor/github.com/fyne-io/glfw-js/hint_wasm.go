//go:build wasm

package glfw

var hints = make(map[Hint]int)

type Hint int

const (
	AlphaBits Hint = iota
	DepthBits
	StencilBits
	Samples

	Focused
	Iconified
	Maximized
	Visible
	Hovered
	Resizable
	Decorated
	Floating
	AutoIconify
	CenterCursor
	TransparentFramebuffer
	FocusOnShow
	ScaleToMonitor

	ClientAPI
	ContextVersionMajor
	ContextVersionMinor
	ContextRobustness
	ContextReleaseBehavior
	OpenGLForwardCompatible
	OpenGLDebugContext
	OpenGLProfile

	// goxjs/glfw-specific hints for WebGL.
	PremultipliedAlpha
	PreserveDrawingBuffer
	PreferLowPowerToHighPerformance
	FailIfMajorPerformanceCaveat
)

func WindowHint(target Hint, hint int) {
	hints[target] = hint
}
