package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// Request is a GraphQL request (GraphQL over HTTP "request parameters").
type Request struct {
	Query         string         `json:"query"`
	OperationName string         `json:"operationName,omitempty"`
	Variables     map[string]any `json:"variables,omitempty"`
	Extensions    map[string]any `json:"extensions,omitempty"`
}

// Response is a GraphQL response (spec §7). Data is absent when the
// request failed before execution (parse, validation, variables), and
// null when a non-null root field failed.
type Response struct {
	Errors     []*Error        `json:"errors,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Extensions map[string]any  `json:"extensions,omitempty"`
}

// Execute parses, validates and executes req. Introspection is allowed;
// the HTTP handler can turn it off.
func (s *Schema) Execute(ctx context.Context, req Request) *Response {
	p, errs := s.prepare(req, true)
	if errs != nil {
		return &Response{Errors: errs}
	}

	return s.run(ctx, p)
}

type prepared struct {
	doc  *document
	op   *operation
	vars map[string]any
}

// prepare runs everything up to execution: parse, validate, limits,
// operation selection and variable coercion.
func (s *Schema) prepare(req Request, introspection bool) (*prepared, []*Error) {
	doc, err := parseQuery(req.Query, s.limits.MaxTokens)
	if err != nil {
		ge, ok := errors.AsType[*Error](err)
		if !ok {
			ge = syntaxError(Location{Line: 1, Column: 1}, err.Error())
		}

		return nil, []*Error{ge}
	}

	if errs := s.validate(doc, introspection); len(errs) > 0 {
		return nil, errs
	}

	op, gerr := selectOperation(doc, req.OperationName)
	if gerr != nil {
		return nil, []*Error{gerr}
	}

	vars, errs := s.coerceVariables(op, req.Variables)
	if errs != nil {
		return nil, errs
	}

	if errs := s.checkLimits(doc, op, vars); len(errs) > 0 {
		return nil, errs
	}

	return &prepared{doc: doc, op: op, vars: vars}, nil
}

func selectOperation(doc *document, name string) (*operation, *Error) {
	if name == "" {
		if len(doc.operations) != 1 {
			return nil, &Error{
				Message:    "Must provide operation name if query contains multiple operations.",
				Extensions: map[string]any{"code": CodeOperationNotFound},
			}
		}

		return doc.operations[0], nil
	}

	for _, op := range doc.operations {
		if op.name == name {
			return op, nil
		}
	}

	return nil, &Error{
		Message:    fmt.Sprintf("Unknown operation named %q.", name),
		Extensions: map[string]any{"code": CodeOperationNotFound},
	}
}

func (s *Schema) coerceVariables(op *operation, given map[string]any) (map[string]any, []*Error) {
	out := map[string]any{}

	var errs []*Error

	for _, vd := range op.vars {
		t := s.typeOf(vd.typ)
		v, present := given[vd.name]

		switch {
		case !present && vd.def != nil:
			c, err := coerceLiteral(vd.def, t, nil)
			if err != nil {
				errs = append(
					errs,
					inputError(fmt.Sprintf("Variable \"$%s\" has invalid default value: %s.", vd.name, err), vd.pos),
				)
				continue
			}

			out[vd.name] = c
		case !present && t.kind == kindNonNull:
			errs = append(errs, inputError(
				fmt.Sprintf("Variable \"$%s\" of required type %q was not provided.", vd.name, t), vd.pos))
		case present:
			c, err := coerceVariable(v, t)
			if err != nil {
				errs = append(
					errs,
					inputError(
						fmt.Sprintf("Variable \"$%s\" got invalid value %s; %s.", vd.name, jsonText(v), err),
						vd.pos,
					),
				)
				continue
			}

			out[vd.name] = c
		}
	}

	return out, errs
}

// executor runs one operation.
type executor struct {
	s     *Schema
	frags map[string]*fragmentDef
	vars  map[string]any
	scope *requestScope

	mu   sync.Mutex
	errs []*Error

	metaFields atomic.Int64 // introspection fields resolved so far
}

// maxIntrospectionFields stops list fan-out inside introspection (each
// __Type level can return every type again), which the static limits
// cannot see; IntrospectionQuery on a large schema stays well below it.
const maxIntrospectionFields = 200_000

// path is a response path, built as a linked list and flattened only when
// an error needs it.
type path struct {
	prev *path
	key  any // string field key or int list index
}

func (p *path) slice() []any {
	var out []any
	for ; p != nil; p = p.prev {
		out = append(out, p.key)
	}

	slices.Reverse(out)

	return out
}

func (p *path) with(key any) *path { return &path{prev: p, key: key} }

func (s *Schema) run(ctx context.Context, p *prepared) *Response {
	if p.op.kind == opSubscription {
		return &Response{Errors: []*Error{{
			Message:    "Subscriptions are not supported over this transport.",
			Locations:  []Location{p.op.pos},
			Extensions: map[string]any{"code": CodeBadRequest},
		}}}
	}

	root := s.query
	if p.op.kind == opMutation {
		root = s.mutation
	}

	return s.execute(ctx, p, root, nil, p.op.kind == opMutation)
}

func (s *Schema) newExecutor(p *prepared) *executor {
	e := &executor{s: s, frags: map[string]*fragmentDef{}, vars: p.vars, scope: newScope()}
	for _, f := range p.doc.fragments {
		e.frags[f.name] = f
	}

	return e
}

// execute runs the operation's selection set on rootValue of root.
func (s *Schema) execute(ctx context.Context, p *prepared, root *namedType, rootValue any, serial bool) *Response {
	e := s.newExecutor(p)
	ctx = withScope(ctx, e.scope)

	obj, ok := e.selectionSet(ctx, p.op.sel, root, rootValue, nil, serial)
	e.scope.end(ctx)

	var data []byte

	if ok {
		var err error
		if data, err = json.Marshal(obj); err != nil {
			e.addError(fmt.Errorf("encode response: %w", err), nil, nil)

			data = []byte("null")
		}
	} else {
		data = []byte("null")
	}

	sortErrors(e.errs)

	return &Response{Errors: e.errs, Data: data}
}

// sortErrors orders errors by path so concurrent resolution stays
// deterministic.
func sortErrors(errs []*Error) {
	slices.SortStableFunc(errs, func(a, b *Error) int {
		return comparePaths(a.Path, b.Path)
	})
}

func comparePaths(a, b []any) int {
	for i := range min(len(a), len(b)) {
		as, bs := fmt.Sprint(a[i]), fmt.Sprint(b[i])
		ai, aInt := a[i].(int)
		bi, bInt := b[i].(int)

		switch {
		case aInt && bInt && ai != bi:
			return ai - bi
		case as < bs:
			return -1
		case as > bs:
			return 1
		}
	}

	return len(a) - len(b)
}

func (e *executor) addError(err error, fields []*field, p *path) {
	ge := fromResolverError(err, e.s.debug)
	if len(fields) > 0 && len(ge.Locations) == 0 {
		ge.Locations = []Location{fields[0].pos}
	}

	if p != nil {
		ge.Path = p.slice()
	}

	if ge.Code() == CodeInternal {
		e.s.log.Error("graphql: resolver error", "path", fmt.Sprint(ge.Path), "error", err)
	}

	e.mu.Lock()
	e.errs = append(e.errs, ge)
	e.mu.Unlock()
}

// object is an ordered JSON object.
type object struct {
	keys []string
	vals []any
}

// MarshalJSON writes the keys in selection order.
func (o *object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer

	b.WriteByte('{')

	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}

		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}

		b.Write(kb)
		b.WriteByte(':')

		vb, err := json.Marshal(o.vals[i])
		if err != nil {
			return nil, err
		}

		b.Write(vb)
	}

	b.WriteByte('}')

	return b.Bytes(), nil
}

type fieldGroup struct {
	key    string
	fields []*field
}

// collectFields implements CollectFields (spec §6.3.2).
func (e *executor) collectFields(
	objType *namedType,
	sel []selection,
	visited map[string]bool,
	groups []*fieldGroup,
) []*fieldGroup {
	for _, s := range sel {
		switch s := s.(type) {
		case *field:
			if !e.included(s.directives) {
				continue
			}

			key := s.name
			if s.alias != "" {
				key = s.alias
			}

			i := slices.IndexFunc(groups, func(g *fieldGroup) bool { return g.key == key })
			if i >= 0 {
				groups[i].fields = append(groups[i].fields, s)
				continue
			}

			groups = append(groups, &fieldGroup{key: key, fields: []*field{s}})
		case *fragmentSpread:
			if visited[s.name] || !e.included(s.directives) {
				continue
			}

			visited[s.name] = true

			f := e.frags[s.name]
			if f == nil || !e.applies(objType, f.typeCond) {
				continue
			}

			groups = e.collectFields(objType, f.sel, visited, groups)
		case *inlineFragment:
			if !e.included(s.directives) || (s.typeCond != "" && !e.applies(objType, s.typeCond)) {
				continue
			}

			groups = e.collectFields(objType, s.sel, visited, groups)
		}
	}

	return groups
}

func (e *executor) applies(objType *namedType, cond string) bool {
	t := e.s.types[cond]
	return t != nil && t.possibleType(objType)
}

// included evaluates @skip and @include.
func (e *executor) included(ds []*directive) bool {
	for _, d := range ds {
		if d.name != "skip" && d.name != "include" {
			continue
		}

		var cond bool

		for _, a := range d.args {
			if a.name == "if" {
				v, _ := coerceLiteral(a.value, nonNull(named(e.s.types["Boolean"])), e.vars)
				cond, _ = v.(bool)
			}
		}

		if (d.name == "skip" && cond) || (d.name == "include" && !cond) {
			return false
		}
	}

	return true
}

// selectionSet executes a selection set on value (spec §6.3). ok is false
// when a non-null field became null and the object must be null too.
func (e *executor) selectionSet(
	ctx context.Context, sel []selection, objType *namedType, value any, p *path, serial bool,
) (*object, bool) {
	// Validation guarantees every field exists; the filter keeps a
	// defensive drop instead of emitting garbage.
	groups := slices.DeleteFunc(e.collectFields(objType, sel, map[string]bool{}, nil), func(g *fieldGroup) bool {
		return e.s.fieldFor(objType, g.fields[0].name) == nil
	})
	obj := &object{keys: make([]string, len(groups)), vals: make([]any, len(groups))}
	oks := make([]bool, len(groups))

	var wg sync.WaitGroup

	for i, g := range groups {
		obj.keys[i] = g.key

		fd := e.s.fieldFor(objType, g.fields[0].name)
		fp := p.with(g.key)

		if serial || !e.s.concurrent || !fd.async {
			obj.vals[i], oks[i] = e.field(ctx, objType, value, g, fd, fp)
			continue
		}

		e.scope.begin()
		wg.Go(func() {
			defer e.scope.end(ctx)
			defer e.recoverInto(g.fields, fp)

			obj.vals[i], oks[i] = e.field(ctx, objType, value, g, fd, fp)
		})
	}

	if e.s.concurrent && !serial {
		e.scope.wait(ctx, &wg)
	}

	if slices.Contains(oks, false) {
		return nil, false
	}

	return obj, true
}

func (e *executor) recoverInto(fields []*field, p *path) {
	if r := recover(); r != nil {
		e.s.log.Error("graphql: panic", "path", fmt.Sprint(p.slice()), "panic", r, "stack", string(debug.Stack()))
		e.addError(fmt.Errorf("panic: %v", r), fields, p)
	}
}

// field executes one field (spec §6.4 ExecuteField).
func (e *executor) field(
	ctx context.Context,
	objType *namedType,
	parent any,
	g *fieldGroup,
	fd *fieldType,
	p *path,
) (any, bool) {
	f := g.fields[0]

	if fd == e.s.metaTypename {
		return objType.name, true
	}

	nullResult := func() (any, bool) { return nil, fd.typ.kind != kindNonNull }

	if strings.HasPrefix(objType.name, "__") {
		switch n := e.metaFields.Add(1); {
		case n == e.s.maxMetaFields+1:
			e.addError(&Error{
				Message: fmt.Sprintf("Introspection resolves more than %d fields.", e.s.maxMetaFields),
				Extensions: map[string]any{
					"code": CodeLimitExceeded, "limit": "introspection fields", "max": e.s.maxMetaFields,
				},
			}, g.fields, p)

			return nullResult()
		case n > e.s.maxMetaFields:
			return nullResult()
		}
	}

	args, err := e.arguments(fd, f)
	if err != nil {
		e.addError(err, g.fields, p)
		return nullResult()
	}

	v, err := e.resolve(ctx, fd, parent, args)
	if err != nil {
		e.addError(err, g.fields, p)
		return nullResult()
	}

	return e.complete(ctx, fd, fd.typ, g.fields, v, p)
}

func (e *executor) resolve(ctx context.Context, fd *fieldType, parent any, args map[string]any) (v any, err error) {
	if fd.resolve == nil {
		return e.s.property(parent, fd.name)
	}

	defer func() {
		if r := recover(); r != nil {
			e.s.log.ErrorContext(ctx, "graphql: resolver panic", "field", fd.parent.name+"."+fd.name, "panic", r,
				"stack", string(debug.Stack()))
			err = fmt.Errorf("resolver %s.%s panicked: %v", fd.parent.name, fd.name, r)
		}
	}()

	return fd.resolve(ctx, parent, args)
}

// arguments implements CoerceArgumentValues (spec §6.4.1).
func (e *executor) arguments(fd *fieldType, f *field) (map[string]any, error) {
	out := make(map[string]any, len(fd.args))

	for _, def := range fd.args {
		var arg *argument

		for _, a := range f.args {
			if a.name == def.name {
				arg = a
			}
		}

		if arg == nil {
			if def.hasDefault {
				out[def.name] = def.defValue
			} else if def.typ.kind == kindNonNull {
				return nil, inputError(
					fmt.Sprintf("Argument %q of required type %q was not provided.", def.name, def.typ),
					f.pos,
				)
			}

			continue
		}

		v, err := coerceLiteral(arg.value, def.typ, e.vars)
		if errors.Is(err, errAbsent) {
			if def.hasDefault {
				out[def.name] = def.defValue
			} else if def.typ.kind == kindNonNull {
				return nil, inputError(
					fmt.Sprintf(
						"Argument %q of required type %q was provided the variable \"$%s\" which was not provided a runtime value.",
						def.name,
						def.typ,
						arg.value.raw,
					),
					arg.pos,
				)
			}

			continue
		}

		if err != nil {
			return nil, inputError(fmt.Sprintf("Argument %q has invalid value: %s.", def.name, err), arg.pos)
		}

		out[def.name] = v
	}

	return out, nil
}

// complete implements CompleteValue (spec §6.4.3) with null propagation:
// ok is false when the null must propagate to the parent.
func (e *executor) complete(ctx context.Context, fd *fieldType, t *gtype, fields []*field, v any, p *path) (any, bool) {
	if t.kind == kindNonNull {
		r, ok := e.completeNullable(ctx, fd, t.ofType, fields, v, p)
		if !ok {
			return nil, false
		}

		if r == nil {
			e.addError(&Error{
				Message:    fmt.Sprintf("Cannot return null for non-nullable field %s.%s.", fd.parent.name, fd.name),
				Extensions: map[string]any{"code": CodeInternal},
			}, fields, p)

			return nil, false
		}

		return r, true
	}

	r, ok := e.completeNullable(ctx, fd, t, fields, v, p)
	if !ok {
		return nil, true
	}

	return r, true
}

// completeNullable completes a value of a nullable type; ok is false when
// an error was recorded and the value must become null.
func (e *executor) completeNullable(
	ctx context.Context,
	fd *fieldType,
	t *gtype,
	fields []*field,
	v any,
	p *path,
) (any, bool) {
	if isNil(v) {
		if t.kind == kindList && isNilSlice(v) {
			return []any{}, true // a nil Go slice is an empty list, not null
		}

		return nil, true
	}

	switch {
	case t.kind == kindList:
		return e.completeList(ctx, fd, t, fields, v, p)
	case t.named.isLeaf():
		out, err := serializeLeaf(t.named, deref(v))
		if err != nil {
			e.addError(err, fields, p)
			return nil, false
		}

		return out, true
	}

	objType := t.named
	if objType.isAbstract() {
		var err error
		if objType, err = e.runtimeType(t.named, v, fd); err != nil {
			e.addError(err, fields, p)
			return nil, false
		}
	}

	var sel []selection
	for _, f := range fields {
		sel = append(sel, f.sel...)
	}

	obj, ok := e.selectionSet(ctx, sel, objType, v, p, false)
	if !ok {
		return nil, false
	}

	return obj, true
}

func (e *executor) completeList(
	ctx context.Context,
	fd *fieldType,
	t *gtype,
	fields []*field,
	v any,
	p *path,
) (any, bool) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}

	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		e.addError(fmt.Errorf("expected a list for field %s.%s, got %T", fd.parent.name, fd.name, v), fields, p)
		return nil, false
	}

	n := rv.Len()
	out := make([]any, n)
	oks := make([]bool, n)

	concurrent := e.s.concurrent && t.ofType.base().isComposite() && n > 1
	if !concurrent {
		for i := range n {
			out[i], oks[i] = e.complete(ctx, fd, t.ofType, fields, rv.Index(i).Interface(), p.with(i))
		}
	} else {
		var wg sync.WaitGroup

		for i := range n {
			ip := p.with(i)
			item := rv.Index(i).Interface()

			e.scope.begin()
			wg.Go(func() {
				defer e.scope.end(ctx)
				defer e.recoverInto(fields, ip)

				out[i], oks[i] = e.complete(ctx, fd, t.ofType, fields, item, ip)
			})
		}

		e.scope.wait(ctx, &wg)
	}

	if slices.Contains(oks, false) {
		return nil, false
	}

	return out, true
}

// runtimeType resolves the object type of a value of an abstract type.
func (e *executor) runtimeType(abstract *namedType, v any, fd *fieldType) (*namedType, error) {
	name := ""

	switch {
	case abstract.resolveType != nil:
		name = abstract.resolveType(v)
	default:
		switch tv := v.(type) {
		case Typenamer:
			name = tv.GraphQLTypename()
		case map[string]any:
			name, _ = tv["__typename"].(string)
		default:
			rt := reflect.TypeOf(v)
			for rt.Kind() == reflect.Pointer {
				rt = rt.Elem()
			}

			name = e.s.goTypes[rt]
		}
	}

	if name == "" && len(abstract.possible) == 1 {
		name = abstract.possible[0].name
	}

	t := e.s.types[name]
	if t == nil || t.kind != kindObject {
		return nil, fmt.Errorf("abstract type %q must resolve to an object type at runtime for field %s.%s (got %T); "+
			"use graphql.Object, graphql.ResolveType or Typenamer", abstract.name, fd.parent.name, fd.name, v)
	}

	if !abstract.possibleType(t) {
		return nil, fmt.Errorf("runtime object type %q is not a possible type for %q", t.name, abstract.name)
	}

	return t, nil
}

func isNil(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() { //nolint:exhaustive // the remaining kinds take the common path below
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func, reflect.Chan:
		return rv.IsNil()
	}

	return false
}

func isNilSlice(v any) bool {
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Slice && rv.IsNil()
}

func deref(v any) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer {
		return v
	}

	if _, ok := v.(fmt.Stringer); ok {
		return v
	}

	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}

		rv = rv.Elem()
	}

	return rv.Interface()
}
