package inertia

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/vukem/web"
)

// PageInfo is a page component and the Go type of its props.
type PageInfo struct {
	Component string
	Props     reflect.Type
}

// Shape returns an anonymous struct type with the JSON the page sends: one
// field per prop, named by its key, with omitempty on the props that are not
// always on the first visit (optional and defer). Code generators read it;
// a prop typed func(context.Context) (T, error) appears as T.
func (p PageInfo) Shape() (reflect.Type, error) {
	plan, err := analyze(p.Props)
	if err != nil {
		return nil, fmt.Errorf("inertia: page %q: %w", p.Component, err)
	}

	return plan.shape(false), nil
}

// SharedShape is [PageInfo.Shape] for Config.SharedType, plus the errors
// prop the adapter sends with every page.
func SharedShape(t reflect.Type) (reflect.Type, error) {
	plan, err := analyze(t)
	if err != nil {
		return nil, fmt.Errorf("inertia: SharedType: %w", err)
	}

	return plan.shape(true), nil
}

// Component is an Inertia page whose props are the struct P.
type Component[P any] struct {
	name string
	plan *propPlan
}

// Define declares the page component with props type P. P must be a struct
// (struct{} for a page without props). It panics on a non-struct P, an
// unknown `inertia` tag option or two fields with the same key, so a mistake
// surfaces at boot.
//
// A field is a prop named by its json tag (or the field name) and holds the
// value, or a func(context.Context) (T, error) resolved only when the prop is
// sent. The `inertia` tag gives the prop a behavior: optional, always,
// defer or defer=<group>, merge, deepmerge, once; defer,merge, defer,once
// and merge,once combine.
func Define[P any](component string) Component[P] {
	if component == "" {
		panic("inertia: Define needs a component name")
	}

	plan, err := analyze(reflect.TypeFor[P]())
	if err != nil {
		panic(fmt.Sprintf("inertia: Define(%q): %v", component, err))
	}

	return Component[P]{name: component, plan: plan}
}

// Name returns the component name ("Projects/Index").
func (p Component[P]) Name() string { return p.name }

// Info describes the page for code generators (Config.Pages).
func (p Component[P]) Info() PageInfo {
	return PageInfo{Component: p.name, Props: reflect.TypeFor[P]()}
}

// Render responds with the page and props, like [Render].
func (p Component[P]) Render(c *web.Ctx, props P) error {
	return p.RenderStatus(c, http.StatusOK, props)
}

// RenderStatus is Render with an HTTP status, like [RenderStatus].
func (p Component[P]) RenderStatus(c *web.Ctx, status int, props P) error {
	return RenderStatus(c, status, p.name, p.plan.props(reflect.ValueOf(props)))
}

// fieldPlan is one prop of a struct, worked out once by analyze.
type fieldPlan struct {
	index     []int
	key       string
	typ       reflect.Type // the type of the value; of T for a func field
	lazy      bool
	omitEmpty bool
	omitZero  bool
	// viaPtr is set for a field promoted through an embedded pointer, which
	// is absent when the pointer is nil.
	viaPtr bool
	depth  int

	kind             propKind
	group            string
	merge, deepMerge bool
	once             bool
}

type propPlan struct{ fields []fieldPlan }

var (
	ctxType = reflect.TypeFor[context.Context]()
	errType = reflect.TypeFor[error]()
)

// analyze reads the props of a struct type: keys by encoding/json's rules and
// behaviors from the `inertia` tag. Embedded structs without a json name are
// flattened, as encoding/json does.
func analyze(t reflect.Type) (*propPlan, error) {
	if t == nil || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("props must be a struct, not %v", t)
	}

	c := &collector{byKey: map[string][]fieldPlan{}, visiting: map[reflect.Type]bool{t: true}}
	if err := c.collect(t, nil, 0, false); err != nil {
		return nil, err
	}

	plan := &propPlan{}

	for _, key := range c.order {
		cands := c.byKey[key]
		slices.SortStableFunc(cands, func(a, b fieldPlan) int { return a.depth - b.depth })

		// As in encoding/json the shallowest field wins; two at the same
		// depth would both vanish there, which here is an error.
		if len(cands) > 1 && cands[0].depth == cands[1].depth {
			return nil, fmt.Errorf("two fields have the key %q", key)
		}

		plan.fields = append(plan.fields, cands[0])
	}

	return plan, nil
}

// collector gathers the candidate fields of a struct and its embedded
// structs, by key.
type collector struct {
	byKey    map[string][]fieldPlan
	order    []string
	visiting map[reflect.Type]bool // the embedded types being read: a type that embeds itself stops here
}

func (c *collector) collect(t reflect.Type, prefix []int, depth int, viaPtr bool) error {
	for i := range t.NumField() {
		f := t.Field(i)
		index := append(append([]int(nil), prefix...), i)

		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Tag.Get("json") == "-" {
			continue
		}

		if f.Anonymous && name == "" {
			handled, err := c.embed(f, index, depth, viaPtr)
			if err != nil {
				return err
			}

			if handled {
				continue
			}
		}

		if !f.IsExported() {
			continue
		}

		if name == "" {
			name = f.Name
		}

		fp := fieldPlan{
			index: index, key: name, typ: f.Type, viaPtr: viaPtr, depth: depth,
			omitEmpty: hasOption(opts, "omitempty"), omitZero: hasOption(opts, "omitzero"),
		}

		if err := fp.check(f); err != nil {
			return err
		}

		if _, seen := c.byKey[name]; !seen {
			c.order = append(c.order, name)
		}

		c.byKey[name] = append(c.byKey[name], fp)
	}

	return nil
}

// embed flattens an embedded struct, as encoding/json does. It reports false
// for an embedded type that is not a struct, which is a field like any other.
func (c *collector) embed(f reflect.StructField, index []int, depth int, viaPtr bool) (bool, error) {
	et, ptr := f.Type, false
	if et.Kind() == reflect.Pointer {
		et, ptr = et.Elem(), true
	}

	if et.Kind() != reflect.Struct {
		return false, nil
	}

	if f.Tag.Get("inertia") != "" {
		return true, fmt.Errorf("field %s: an inertia tag on an embedded struct does nothing; tag its fields", f.Name)
	}

	if c.visiting[et] {
		return true, nil
	}

	c.visiting[et] = true
	defer delete(c.visiting, et)

	return true, c.collect(et, index, depth+1, viaPtr || ptr)
}

// check reads the func form and the `inertia` tag of f and refuses what
// would silently hide the prop.
func (fp *fieldPlan) check(f reflect.StructField) error {
	if fp.key == "errors" {
		return fmt.Errorf("field %s: the key \"errors\" is the validation errors the server always sets", f.Name)
	}

	if err := fp.readFunc(f); err != nil {
		return err
	}

	tag := f.Tag.Get("inertia")
	if tag != "" && (fp.omitEmpty || fp.omitZero) {
		return fmt.Errorf("field %s: omitempty and omitzero drop the prop, which an inertia option needs sent", f.Name)
	}

	if err := fp.readTag(tag); err != nil {
		return fmt.Errorf("field %s: %w", f.Name, err)
	}

	return nil
}

func hasOption(opts, want string) bool {
	for o := range strings.SplitSeq(opts, ",") {
		if o == want {
			return true
		}
	}

	return false
}

// readFunc recognizes func(context.Context) (T, error), the lazy form.
func (fp *fieldPlan) readFunc(f reflect.StructField) error {
	if f.Type.Kind() != reflect.Func {
		return nil
	}

	ft := f.Type
	if ft.NumIn() != 1 || !ctxType.AssignableTo(ft.In(0)) || ft.NumOut() != 2 || ft.Out(1) != errType ||
		ft.IsVariadic() {
		return fmt.Errorf("field %s: a func prop must be func(context.Context) (T, error)", f.Name)
	}

	fp.lazy, fp.typ = true, ft.Out(0)

	return nil
}

// readTag parses the `inertia` tag. optional and always stand alone; defer,
// merge or deepmerge and once combine as defer,merge, defer,once and
// merge,once.
func (fp *fieldPlan) readTag(tag string) error {
	if tag == "" {
		return nil
	}

	var kinds, shapes int

	for opt := range strings.SplitSeq(tag, ",") {
		switch name, group, hasGroup := strings.Cut(opt, "="); {
		case name == "optional" && !hasGroup:
			fp.kind, kinds = kindOptional, kinds+1
		case name == "always" && !hasGroup:
			fp.kind, kinds = kindAlways, kinds+1
		case name == "defer":
			fp.kind, fp.group, kinds = kindDefer, "default", kinds+1
			if hasGroup {
				if group == "" {
					return errors.New("defer= needs a group name")
				}

				fp.group = group
			}
		case name == "merge" && !hasGroup:
			fp.merge, shapes = true, shapes+1
		case name == "deepmerge" && !hasGroup:
			fp.merge, fp.deepMerge, shapes = true, true, shapes+1
		case name == "once" && !hasGroup:
			fp.once = true
		default:
			return fmt.Errorf("unknown inertia option %q", opt)
		}
	}

	standalone := fp.kind == kindOptional || fp.kind == kindAlways
	if kinds > 1 || shapes > 1 || (standalone && (fp.merge || fp.once)) {
		return fmt.Errorf("inertia:%q combines options that do not go together", tag)
	}

	return nil
}

// props turns a value of the struct into Props, wrapping each prop in the
// behavior its tag asks for.
func (pl *propPlan) props(v reflect.Value) Props {
	out := make(Props, len(pl.fields))

	for _, f := range pl.fields {
		fv, err := v.FieldByIndexErr(f.index)
		if err != nil {
			continue // a nil embedded pointer: its props are absent, as in JSON
		}

		if (f.omitEmpty && isEmptyValue(fv)) || (f.omitZero && fv.IsZero()) {
			continue
		}

		var val any

		if f.lazy {
			if fv.IsNil() {
				continue
			}

			val = lazyProp(fv)
		} else {
			val = emptyIfNil(fv)
		}

		out[f.key] = f.wrap(val)
	}

	return out
}

// lazyProp adapts func(context.Context) (T, error) to the form the resolver
// calls, only when the prop is sent.
func lazyProp(fn reflect.Value) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		res := fn.Call([]reflect.Value{reflect.ValueOf(ctx)})
		if err, _ := reflect.TypeAssert[error](res[1]); err != nil {
			return nil, err
		}

		return emptyIfNil(res[0]), nil
	}
}

// emptyIfNil gives a nil slice or map as an empty one: the TypeScript type
// is T[] or Record, never null. Values nested in a struct are left as they are.
func emptyIfNil(v reflect.Value) any {
	switch {
	case v.Kind() == reflect.Slice && v.IsNil():
		return reflect.MakeSlice(v.Type(), 0, 0).Interface()
	case v.Kind() == reflect.Map && v.IsNil():
		return reflect.MakeMap(v.Type()).Interface()
	default:
		return v.Interface()
	}
}

// wrap builds the same *Prop Optional, Always, Defer, Merge, DeepMerge and
// Once build.
func (fp fieldPlan) wrap(v any) any {
	if fp.kind == kindValue && !fp.merge && !fp.once {
		return v
	}

	p := &Prop{value: v, kind: fp.kind, merge: fp.merge, deepMerge: fp.deepMerge, once: fp.once}
	if fp.kind == kindDefer {
		p.group = fp.group
	}

	return p
}

// isEmptyValue is encoding/json's omitempty test.
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer, reflect.Func:
		return v.IsZero()
	default:
		return false
	}
}

// shape builds the anonymous struct of the JSON the props produce.
func (pl *propPlan) shape(withErrors bool) reflect.Type {
	fields := make([]reflect.StructField, 0, len(pl.fields)+1)

	for i, f := range pl.fields {
		tag := f.key
		if f.omitEmpty || f.viaPtr || f.lazy || f.kind == kindOptional || f.kind == kindDefer {
			tag += ",omitempty"
		}

		if f.omitZero {
			tag += ",omitzero"
		}

		fields = append(fields, reflect.StructField{
			Name: "F" + strconv.Itoa(i), Type: f.typ, Tag: reflect.StructTag("json:" + strconv.Quote(tag)),
		})
	}

	if withErrors {
		fields = append(fields, reflect.StructField{
			Name: "Errors", Type: reflect.TypeFor[map[string]string](), Tag: `json:"errors"`,
		})
	}

	return reflect.StructOf(fields)
}
