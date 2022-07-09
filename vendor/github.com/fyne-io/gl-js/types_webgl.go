// Copyright 2014 The Go Authors.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js && !wasm
// +build js,!wasm

package gl

import "github.com/gopherjs/gopherjs/js"

type Enum int

type Attrib struct {
	Value int
}

type Program struct {
	*js.Object
}

type Shader struct {
	*js.Object
}

type Buffer struct {
	*js.Object
}

type Framebuffer struct {
	*js.Object
}

type Renderbuffer struct {
	*js.Object
}

type Texture struct {
	*js.Object
}

type Uniform struct {
	*js.Object
}

var NoAttrib Attrib
var NoProgram Program
var NoShader Shader
var NoBuffer Buffer
var NoFramebuffer Framebuffer
var NoRenderbuffer Renderbuffer
var NoTexture Texture
var NoUniform Uniform

// Object is a generic interface for OpenGL objects
type Object interface {
	Identifier() Enum
	Name() *js.Object
}

// Implement Name() for the Object interface
func (p Program) Name() *js.Object {
	return p.Object
}

func (s Shader) Name() *js.Object {
	return s.Object
}

func (b Buffer) Name() *js.Object {
	return b.Object
}

func (fb Framebuffer) Name() *js.Object {
	return fb.Object
}

func (rb Renderbuffer) Name() *js.Object {
	return rb.Object
}

func (t Texture) Name() *js.Object {
	return t.Object
}
