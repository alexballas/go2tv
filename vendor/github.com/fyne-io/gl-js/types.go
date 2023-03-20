//go:build !wasm
// +build !wasm

package gl

func (v Attrib) IsValid() bool       { return v != NoAttrib }
func (v Program) IsValid() bool      { return v != NoProgram }
func (v Shader) IsValid() bool       { return v != NoShader }
func (v Buffer) IsValid() bool       { return v != NoBuffer }
func (v Framebuffer) IsValid() bool  { return v != NoFramebuffer }
func (v Renderbuffer) IsValid() bool { return v != NoRenderbuffer }
func (v Texture) IsValid() bool      { return v != NoTexture }
func (v Uniform) IsValid() bool      { return v != NoUniform }

// Implement Identifier() for the Object interface
func (p Program) Identifier() Enum {
	return PROGRAM
}

func (s Shader) Identifier() Enum {
	return SHADER
}

func (b Buffer) Identifier() Enum {
	return BUFFER
}

func (fb Framebuffer) Identifier() Enum {
	return FRAMEBUFFER
}

func (rb Renderbuffer) Identifier() Enum {
	return RENDERBUFFER
}

func (t Texture) Identifier() Enum {
	return TEXTURE
}
