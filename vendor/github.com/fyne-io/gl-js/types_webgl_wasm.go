// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && wasm
// +build js,wasm

package gl

import "syscall/js"

type Enum int

type Attrib struct {
	Value int
}

type Program struct {
	js.Value
}

type Shader struct {
	js.Value
}

type Buffer struct {
	js.Value
}

type Framebuffer struct {
	js.Value
}

type Renderbuffer struct {
	js.Value
}

type Texture struct {
	js.Value
}

type Uniform struct {
	js.Value
}

var NoAttrib Attrib
var NoProgram = Program{js.Null()}
var NoShader = Shader{js.Null()}
var NoBuffer = Buffer{js.Null()}
var NoFramebuffer = Framebuffer{js.Null()}
var NoRenderbuffer = Renderbuffer{js.Null()}
var NoTexture = Texture{js.Null()}
var NoUniform = Uniform{js.Null()}

func (v Attrib) IsValid() bool       { return v != NoAttrib }
func (v Program) IsValid() bool      { return !v.Equal(NoProgram.Value) }
func (v Shader) IsValid() bool       { return !v.Equal(NoShader.Value) }
func (v Buffer) IsValid() bool       { return !v.Equal(NoBuffer.Value) }
func (v Framebuffer) IsValid() bool  { return !v.Equal(NoFramebuffer.Value) }
func (v Renderbuffer) IsValid() bool { return !v.Equal(NoRenderbuffer.Value) }
func (v Texture) IsValid() bool      { return !v.Equal(NoTexture.Value) }
func (v Uniform) IsValid() bool      { return !v.Equal(NoUniform.Value) }

// Object is a generic interface for OpenGL objects
type Object interface {
	Identifier() Enum
	Name() js.Value
}

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

// Implement Name() for the Object interface
func (p Program) Name() js.Value {
	return p.Value
}

func (s Shader) Name() js.Value {
	return s.Value
}

func (b Buffer) Name() js.Value {
	return b.Value
}

func (fb Framebuffer) Name() js.Value {
	return fb.Value
}

func (rb Renderbuffer) Name() js.Value {
	return rb.Value
}

func (t Texture) Name() js.Value {
	return t.Value
}
