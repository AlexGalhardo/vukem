package view

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/AlexGalhardo/vukem/web"
)

// Page is a view whose data is T. It renders like web.Ctx.Render and
// web.Ctx.Fragment, and declares T for view:check (see Decl).
type Page[T any] struct {
	name   string
	blocks []string
}

// Of declares the view name with data type T and the blocks handlers render
// on their own (htmx fragments). Use struct{} for a view without data.
func Of[T any](name string, blocks ...string) Page[T] {
	return Page[T]{name: name, blocks: slices.Clone(blocks)}
}

// Render renders the page with data, like web.Ctx.Render. Options such as
// web.Block pass through unchecked: view:check does not see which block a
// call asks for, only the blocks declared in Of.
//
// view:check accepts a method with a pointer receiver on a value it could
// not call at run time (a known false negative: it reads signatures only).
func (p Page[T]) Render(c *web.Ctx, data T, opts ...web.RenderOption) error {
	return c.Render(p.name, data, opts...)
}

// Fragment renders one declared block, like web.Ctx.Fragment. A block that
// was not declared in Of is an error.
func (p Page[T]) Fragment(c *web.Ctx, block string, data T, opts ...web.RenderOption) error {
	if !slices.Contains(p.blocks, block) {
		return fmt.Errorf("view: %q has no declared block %q", p.name, block)
	}

	return c.Fragment(p.name, block, data, opts...)
}

// Decl describes the page for view:check.
func (p Page[T]) Decl() Decl {
	return Decl{Name: p.name, Data: reflect.TypeFor[T](), Blocks: slices.Clone(p.blocks)}
}

// Decl is a view name, the Go type of its data and its fragment blocks.
type Decl struct {
	Name   string
	Data   reflect.Type
	Blocks []string
}
