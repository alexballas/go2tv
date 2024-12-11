//go:build !wasm

package glfw

import "github.com/go-gl/glfw/v3.3/glfw"

type Hint int

const (
	AlphaBits   = Hint(glfw.AlphaBits)
	DepthBits   = Hint(glfw.DepthBits)
	StencilBits = Hint(glfw.StencilBits)
	Samples     = Hint(glfw.Samples)

	Focused                = Hint(glfw.Focused)
	Iconified              = Hint(glfw.Iconified)
	Maximized              = Hint(glfw.Maximized)
	Visible                = Hint(glfw.Visible)
	Hovered                = Hint(glfw.Hovered)
	Resizable              = Hint(glfw.Resizable)
	Decorated              = Hint(glfw.Decorated)
	Floating               = Hint(glfw.Floating)
	AutoIconify            = Hint(glfw.AutoIconify)
	CenterCursor           = Hint(glfw.CenterCursor)
	TransparentFramebuffer = Hint(glfw.TransparentFramebuffer)
	FocusOnShow            = Hint(glfw.FocusOnShow)
	ScaleToMonitor         = Hint(glfw.ScaleToMonitor)

	ClientAPI               = Hint(glfw.ClientAPI)
	ContextVersionMajor     = Hint(glfw.ContextVersionMajor)
	ContextVersionMinor     = Hint(glfw.ContextVersionMinor)
	ContextRobustness       = Hint(glfw.ContextRobustness)
	ContextReleaseBehavior  = Hint(glfw.ContextReleaseBehavior)
	OpenGLForwardCompatible = Hint(glfw.OpenGLForwardCompatible)
	OpenGLDebugContext      = Hint(glfw.OpenGLDebugContext)
	OpenGLProfile           = Hint(glfw.OpenGLProfile)

	// These hints used for WebGL contexts, ignored on desktop.
	PremultipliedAlpha = noopHint
	PreserveDrawingBuffer
	PreferLowPowerToHighPerformance
	FailIfMajorPerformanceCaveat
)

// noopHint is ignored.
const noopHint Hint = -1

func WindowHint(target Hint, hint int) {
	if target == noopHint {
		return
	}

	glfw.WindowHint(glfw.Hint(target), hint)
}
