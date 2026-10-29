package admin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/naming"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/z"
)

// AnyResource is a Resource of any model, as accepted by Panel.Register.
// Only Resource implements it.
type AnyResource interface {
	info() *base
	check() error
	subject() any
	count(ctx context.Context, ex database.Executor) (int64, error)
	routes(p *Panel, g *router.Group)
}

// Input types a form field can be rendered with (Resource.Input).
const (
	InputText     = "text"
	InputTextarea = "textarea"
	InputNumber   = "number"
	InputEmail    = "email"
	InputURL      = "url"
	InputPassword = "password"
	InputCheckbox = "checkbox"
	InputSelect   = "select"
	InputDate     = "date"
	InputDateTime = "datetime-local"
)

var (
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

	// sensitiveRe flags columns that hold secrets by name; json:"-" fields
	// are sensitive too. Sensitive columns are never shown, searched,
	// sorted, filtered or written to the audit log. It matches anywhere in
	// the name (fail closed); nonSecretRe exempts only known counters.
	sensitiveRe = regexp.MustCompile(`(?i)(password|passwd|secret|token|api_?key|private_?key|recovery|otp|totp)`)

	// nonSecretRe lists names that contain a secret word but hold counts:
	// token usage counters and "footprint" (which contains "otp").
	nonSecretRe = regexp.MustCompile(
		`(?i)^(tokens_(used|count|total|remaining|limit)|max_tokens(_[a-z0-9_]+)?|[a-z0-9_]*_token_count|[a-z0-9_]*footprint[a-z0-9_]*)$`,
	)

	protected = []string{"created_at", "updated_at", "deleted_at"}
)

func sensitiveName(col string) bool {
	return sensitiveRe.MatchString(col) && !nonSecretRe.MatchString(col)
}

type column struct {
	name  string
	label string
	index []int
	typ   reflect.Type
}

type input struct {
	key    string
	column string
	label  string
	src    []int // field index in the schema's struct
	assign func(dst, src reflect.Value)
	kind   string // input type
	attrs  map[string]any
	req    bool
	enum   []string
}

type filter struct {
	column  string
	options []string
}

// base is the model-independent part of a resource.
type base struct {
	slug, singular, plural string
	pk                     string
	columns                []column
	byName                 map[string]*column
	inputs                 []input
	list                   []string
	search                 []string
	sortable               []string
	filters                []filter
	perPage                int
	hidden, readonly       map[string]bool
	sensitive              map[string]bool
	errs                   []error
}

// Resource exposes model T in the admin panel: a paginated list with
// search, sort and filters, a detail page, and create/edit forms generated
// from a z schema. Options chain and return the resource; mistakes (unknown
// columns, sensitive columns in lists) are reported by Panel.Register, at
// boot.
type Resource[T any] struct {
	base

	parse      func(in map[string]any) (reflect.Value, error)
	computed   []computed[T]
	beforeSave []func(ctx context.Context, rec *T) error
	actions    []Action[T]
}

type computed[T any] struct {
	label string
	fn    func(T) string
}

// NewResource returns a resource for model T whose forms are generated
// from schema: each schema key is a form field written to the model column
// with the same name (or the snake_case of the input field name). Only
// schema keys are ever written (mass-assignment protection); the primary
// key and timestamps cannot be schema keys. It panics on a model with a
// composite primary key (orm.CompositeKeyer). T is given explicitly and I is
// inferred:
//
//	admin.NewResource[models.Post](requests.PostSchema)
func NewResource[T, I any](schema z.StructSchema[I]) *Resource[T] {
	if keys := orm.PrimaryKeysOf[T](); len(keys) > 1 {
		// Rows are addressed by one id in URLs and forms.
		panic(
			fmt.Sprintf(
				"admin: %s has a composite primary key %v; resources need a single-column key",
				orm.TableOf[T](),
				keys,
			),
		)
	}

	r := &Resource[T]{}
	r.pk = orm.PrimaryKeyOf[T]()
	r.byName = map[string]*column{}
	r.hidden, r.readonly, r.sensitive = map[string]bool{}, map[string]bool{}, map[string]bool{}
	r.perPage = 20

	table := orm.TableOf[T]()
	r.slug = naming.Kebab(table)
	r.plural = humanize(table)
	r.singular = humanize(naming.Snake(reflect.TypeFor[T]().Name()))

	for _, f := range orm.FieldsOf[T]() {
		r.columns = append(r.columns, column{name: f.Column, label: humanize(f.Column), index: f.Index, typ: f.Type})

		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name == "-" || sensitiveName(f.Column) {
			r.sensitive[f.Column] = true
		}
	}

	for i := range r.columns {
		r.byName[r.columns[i].name] = &r.columns[i]
	}

	r.parse = func(in map[string]any) (reflect.Value, error) {
		v, err := schema.Parse(in)
		return reflect.ValueOf(v), err
	}

	js := z.JSONSchema(schema)
	props, _ := js["properties"].(map[string]any)
	required, _ := js["required"].([]string)

	for _, key := range schema.Keys() {
		sf, _ := schema.Field(key)
		prop, _ := props[key].(map[string]any)

		in, err := r.newInput(key, sf, prop, slices.Contains(required, key))
		if err != nil {
			r.errs = append(r.errs, err)
			continue
		}

		r.inputs = append(r.inputs, in)
	}

	return r
}

func (r *Resource[T]) newInput(key string, sf reflect.StructField, prop map[string]any, req bool) (input, error) {
	col := key
	if _, ok := r.byName[col]; !ok {
		col = naming.Snake(sf.Name)
	}

	c, ok := r.byName[col]
	if !ok {
		return input{}, fmt.Errorf("schema key %q matches no column of %s", key, r.plural)
	}

	if col == r.pk || slices.Contains(protected, col) {
		return input{}, fmt.Errorf("schema key %q writes the protected column %q", key, col)
	}

	assign, err := assigner(c.typ, sf.Type)
	if err != nil {
		return input{}, fmt.Errorf("schema key %q: %w", key, err)
	}

	in := input{key: key, column: col, label: c.label, src: sf.Index, assign: assign, attrs: prop, req: req}
	in.kind, in.enum = inputKind(prop, c.typ)

	return in, nil
}

// assigner copies a parsed input field (src type) into a model field (dst
// type), or reports why it cannot.
func assigner(dst, src reflect.Type) (func(dst, src reflect.Value), error) {
	nullTime := reflect.TypeFor[orm.NullTime]()
	timeType := reflect.TypeFor[time.Time]()

	switch {
	case src.AssignableTo(dst):
		return func(d, s reflect.Value) { d.Set(s) }, nil
	case src.ConvertibleTo(dst) && src.Kind() == dst.Kind():
		return func(d, s reflect.Value) { d.Set(s.Convert(dst)) }, nil
	case dst == nullTime && src == timeType:
		return func(d, s reflect.Value) {
			t, _ := reflect.TypeAssert[time.Time](s)
			d.Set(reflect.ValueOf(nullOf(t, !t.IsZero())))
		}, nil
	case dst == nullTime && src == reflect.PointerTo(timeType):
		return func(d, s reflect.Value) {
			if s.IsNil() {
				d.Set(reflect.Zero(dst))
				return
			}

			t, _ := reflect.TypeAssert[time.Time](s.Elem())
			d.Set(reflect.ValueOf(nullOf(t, true)))
		}, nil
	case src.Kind() == reflect.Pointer && src.Elem().AssignableTo(dst):
		return func(d, s reflect.Value) {
			if s.IsNil() {
				d.Set(reflect.Zero(dst))
				return
			}

			d.Set(s.Elem())
		}, nil
	case dst.Kind() == reflect.Pointer && src.AssignableTo(dst.Elem()):
		return func(d, s reflect.Value) {
			p := reflect.New(dst.Elem())
			p.Elem().Set(s)
			d.Set(p)
		}, nil
	}

	return nil, fmt.Errorf("cannot write %s into a %s column", src, dst)
}

func nullOf(t time.Time, valid bool) orm.NullTime {
	var n orm.NullTime
	n.V, n.Valid = t, valid

	return n
}

// inputKind picks the form control from the field's JSON Schema and the
// model column type.
func inputKind(prop map[string]any, col reflect.Type) (string, []string) {
	if enum, ok := prop["enum"].([]any); ok {
		var opts []string

		for _, v := range enum {
			if s, ok := v.(string); ok {
				opts = append(opts, s)
			}
		}

		return InputSelect, opts
	}

	if enum, ok := prop["enum"].([]string); ok {
		return InputSelect, enum
	}

	typ, _ := prop["type"].(string)
	if types, ok := prop["type"].([]any); ok && len(types) > 0 {
		typ, _ = types[0].(string)
	}

	switch typ {
	case "boolean":
		return InputCheckbox, nil
	case "integer", "number":
		return InputNumber, nil
	}

	switch prop["format"] {
	case "email":
		return InputEmail, nil
	case "uri":
		return InputURL, nil
	case "date-time":
		return InputDateTime, nil
	}

	if col == reflect.TypeFor[time.Time]() || col == reflect.TypeFor[orm.NullTime]() {
		return InputDateTime, nil
	}

	if n, ok := prop["maxLength"].(int); ok && n <= 255 {
		return InputText, nil
	}

	return InputTextarea, nil
}

// humanize turns "created_at" into "Created at".
func humanize(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "_", " "), "-", " "))
	if s == "" {
		return s
	}

	if strings.EqualFold(s, "id") {
		return "ID"
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

func (r *Resource[T]) fail(format string, args ...any) *Resource[T] {
	r.errs = append(r.errs, fmt.Errorf(format, args...))
	return r
}

func (r *Resource[T]) known(cols []string) bool {
	for _, c := range cols {
		if _, ok := r.byName[c]; !ok {
			r.fail("unknown column %q", c)
			return false
		}
	}

	return true
}

// Slug sets the URL segment (default: the table name in kebab-case).
func (r *Resource[T]) Slug(slug string) *Resource[T] {
	if !slugRe.MatchString(slug) || strings.HasPrefix(slug, "_") {
		return r.fail("invalid slug %q", slug)
	}

	r.slug = slug

	return r
}

// Labels sets the singular and plural names shown in the UI.
func (r *Resource[T]) Labels(singular, plural string) *Resource[T] {
	r.singular, r.plural = singular, plural
	return r
}

// Label sets the label of a column.
func (r *Resource[T]) Label(col, label string) *Resource[T] {
	if !r.known([]string{col}) {
		return r
	}

	r.byName[col].label = label

	for i := range r.inputs {
		if r.inputs[i].column == col {
			r.inputs[i].label = label
		}
	}

	return r
}

// Hidden hides columns from the list, the detail page and the forms;
// hidden schema keys are never written.
func (r *Resource[T]) Hidden(cols ...string) *Resource[T] {
	if r.known(cols) {
		for _, c := range cols {
			r.hidden[c] = true
		}
	}

	return r
}

// ReadOnly shows columns in the edit form without letting the admin change
// them: read-only schema keys keep the stored value and are never written.
func (r *Resource[T]) ReadOnly(cols ...string) *Resource[T] {
	if r.known(cols) {
		for _, c := range cols {
			r.readonly[c] = true
		}
	}

	return r
}

// Sensitive marks columns holding secrets (password hashes, tokens) beyond
// the ones detected by name or json:"-": they are never displayed, listed,
// searched, sorted, filtered or written to the audit log. A sensitive
// schema key renders as an empty password field and keeps the stored value
// when left blank on edit.
func (r *Resource[T]) Sensitive(cols ...string) *Resource[T] {
	if r.known(cols) {
		for _, c := range cols {
			r.sensitive[c] = true
		}
	}

	return r
}

// List sets the columns of the list, in order (default: every column that
// is not hidden or sensitive).
func (r *Resource[T]) List(cols ...string) *Resource[T] {
	if r.known(cols) {
		r.list = slices.Clone(cols)
	}

	return r
}

// Column adds a computed column to the list; fn's result is HTML-escaped.
func (r *Resource[T]) Column(label string, fn func(T) string) *Resource[T] {
	r.computed = append(r.computed, computed[T]{label: label, fn: fn})
	return r
}

// Search enables the search box over the given string columns
// (case-insensitive LIKE with bound parameters).
func (r *Resource[T]) Search(cols ...string) *Resource[T] {
	if r.known(cols) {
		r.search = slices.Clone(cols)
	}

	return r
}

// Sortable sets the columns the list may be sorted by (default: the listed
// columns). Any other sort parameter is ignored.
func (r *Resource[T]) Sortable(cols ...string) *Resource[T] {
	if r.known(cols) {
		r.sortable = slices.Clone(cols)
	}

	return r
}

// Filter adds an exact-match filter on col; with options it renders a
// select, otherwise a text box. Boolean columns get yes/no options.
func (r *Resource[T]) Filter(col string, options ...string) *Resource[T] {
	if r.known([]string{col}) {
		r.filters = append(r.filters, filter{column: col, options: options})
	}

	return r
}

// PerPage sets the page size of the list (default 20, at most 200).
func (r *Resource[T]) PerPage(n int) *Resource[T] {
	if n < 1 || n > 200 {
		return r.fail("per page %d out of range 1..200", n)
	}

	r.perPage = n

	return r
}

// Input overrides the form control of a schema key (one of the Input*
// constants).
func (r *Resource[T]) Input(key, kind string) *Resource[T] {
	valid := []string{
		InputText, InputTextarea, InputNumber, InputEmail, InputURL, InputPassword,
		InputCheckbox, InputSelect, InputDate, InputDateTime,
	}
	if !slices.Contains(valid, kind) {
		return r.fail("unknown input type %q", kind)
	}

	for i := range r.inputs {
		if r.inputs[i].key == key {
			r.inputs[i].kind = kind
			return r
		}
	}

	return r.fail("unknown schema key %q", key)
}

// BeforeSave runs fn on the record before every create and update (after
// validation, inside the transaction), e.g. to hash a password or set an
// owner. Returning an error aborts the write.
func (r *Resource[T]) BeforeSave(fn func(ctx context.Context, rec *T) error) *Resource[T] {
	r.beforeSave = append(r.beforeSave, fn)
	return r
}

func (r *Resource[T]) info() *base { return &r.base }

// check validates the configuration (Panel.Register calls it).
func (r *Resource[T]) check() error {
	errs := slices.Clone(r.errs)

	if len(r.list) == 0 {
		for _, c := range r.columns {
			if !r.hidden[c.name] && !r.sensitive[c.name] {
				r.list = append(r.list, c.name)
			}
		}
	}

	if len(r.sortable) == 0 {
		r.sortable = slices.Clone(r.list)
	}

	for what, cols := range map[string][]string{"listed": r.list, "searched": r.search, "sorted": r.sortable} {
		for _, c := range cols {
			if r.sensitive[c] {
				errs = append(errs, fmt.Errorf("sensitive column %q cannot be %s", c, what))
			}
		}
	}

	for _, f := range r.filters {
		if r.sensitive[f.column] {
			errs = append(errs, fmt.Errorf("sensitive column %q cannot be filtered", f.column))
		}

		if t := r.byName[f.column].typ; !filterable(t) {
			errs = append(errs, fmt.Errorf("column %q of type %s cannot be filtered", f.column, t))
		}
	}

	for _, c := range r.search {
		if r.byName[c].typ.Kind() != reflect.String {
			errs = append(errs, fmt.Errorf("column %q is not a string and cannot be searched", c))
		}
	}

	for i := range r.inputs {
		if r.sensitive[r.inputs[i].column] {
			r.inputs[i].kind = InputPassword
		}
	}

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("admin: resource %s: %w", r.slug, err)
	}

	return nil
}
