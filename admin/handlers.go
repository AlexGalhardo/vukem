package admin

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

type resourceView struct {
	Singular, Plural, URL, CreateURL string
}

type listView struct {
	Columns    []header
	Rows       []row
	Search     string
	Searchable bool
	Filters    []filterView
	Sort, Dir  string
	Total      int64
	ColSpan    int
	Pager      pager
}

type header struct {
	Label, SortURL, Sorted string
}

type row struct {
	ID                       string
	Cells                    []string
	ShowURL, EditURL, DelURL string
}

type filterView struct {
	Label, Param, Value string
	Options             []option
}

type option struct {
	Value, Label string
	Selected     bool
}

type pager struct {
	Page, Last       int
	PrevURL, NextURL string
}

type recordView struct {
	ID              string
	Fields          []fieldView
	EditURL, DelURL string
	DeleteAction    string
	Cancel          string
}

type fieldView struct {
	Label, Value string
}

type formView struct {
	Action, Cancel, Submit string
	Fields                 []formField
	Errors                 []string
}

type formField struct {
	Key, ID, Label, Type, Value string
	Checked, Required, ReadOnly bool
	Options                     []string
	Error                       string
	MaxLength, Min, Max         string
}

func (r *Resource[T]) subject() any { return (*T)(nil) }

func (r *Resource[T]) count(ctx context.Context, ex database.Executor) (int64, error) {
	return orm.QueryOf[T](ex).Count(ctx)
}

func (r *Resource[T]) routes(p *Panel, g *router.Group) {
	g.Group("/"+r.slug, func(g *router.Group) {
		g.Name(r.slug + ".")
		g.Get("", r.index(p)).Name("index")
		g.Get("/create", r.create(p)).Name("create")
		g.Post("", r.store(p)).Name("store")
		g.Get("/{id}", r.show(p)).Name("show")
		g.Get("/{id}/edit", r.edit(p)).Name("edit")
		g.Post("/{id}", r.update(p)).Name("update")
		g.Get("/{id}/delete", r.confirmDelete(p)).Name("delete")
		g.Post("/{id}/delete", r.destroy(p)).Name("destroy")
	})
}

func (r *Resource[T]) view(c *web.Ctx, p *Panel) *resourceView {
	v := &resourceView{Singular: r.singular, Plural: r.plural, URL: p.url(r.slug)}
	if p.can(c, AbilityCreate, r.subject()) {
		v.CreateURL = p.url(r.slug, "create")
	}

	return v
}

func (r *Resource[T]) id(rv reflect.Value) string {
	return formValue(rv.FieldByIndex(r.byName[r.pk].index))
}

// auditColumns are the columns compared for the audit log.
func (r *Resource[T]) auditColumns() []string {
	var cols []string

	for _, c := range r.columns {
		if c.name != r.pk && !slices.Contains(protected, c.name) {
			cols = append(cols, c.name)
		}
	}

	return cols
}

func (r *Resource[T]) find(c *web.Ctx, p *Panel) (*T, error) {
	id, ok := keyValue(r.byName[r.pk].typ, c.Param("id"))
	if !ok {
		return nil, orm.ErrNotFound
	}

	rec, err := orm.QueryOf[T](p.cfg.DB).Find(c.Context(), id)
	if err != nil {
		return nil, err
	}

	return &rec, nil
}

func (r *Resource[T]) index(p *Panel) web.HandlerFunc {
	return func(c *web.Ctx) error {
		if err := p.authorize(c, AbilityViewAny, r.subject()); err != nil {
			return err
		}

		qs := c.Request().URL.Query()
		base := p.url(r.slug)
		q := orm.QueryOf[T](p.cfg.DB)
		lv := &listView{Searchable: len(r.search) > 0}

		if s := strings.TrimSpace(qs.Get("q")); s != "" && lv.Searchable {
			lv.Search = s
			q = r.searchQuery(q, p.cfg.DB.Dialect(), s)
		}

		for _, f := range r.filters {
			fv, val, ok := r.filterView(f, qs)
			if ok {
				q = q.Where(f.column, val)
			}

			lv.Filters = append(lv.Filters, fv)
		}

		lv.Sort, lv.Dir = qs.Get("sort"), "asc"
		if qs.Get("dir") == "desc" {
			lv.Dir = "desc"
		}

		// Only allowlisted columns reach ORDER BY; anything else falls back
		// to the default order (A05).
		if slices.Contains(r.sortable, lv.Sort) {
			q = q.OrderBy(lv.Sort, orm.Direction(strings.ToUpper(lv.Dir)))
		} else {
			lv.Sort = ""
		}

		if lv.Sort != r.pk {
			q = q.OrderBy(r.pk, orm.Desc)
		}

		pg, err := q.MaxPerPage(r.perPage).Paginate(c.Context(), max(c.QueryInt("page", 1), 1), r.perPage)
		if err != nil {
			return err
		}

		lv.Total = pg.Total
		lv.Pager = newPager(base, qs, pg.Page, pg.LastPage)
		lv.Columns = r.headers(base, qs, lv)
		lv.ColSpan = len(lv.Columns) + 1

		for i := range pg.Items {
			lv.Rows = append(lv.Rows, r.row(c, p, &pg.Items[i]))
		}

		data := p.base(c, r.plural, r.slug)
		data.Resource, data.List = r.view(c, p), lv

		return p.render(c, http.StatusOK, "index", "list", data)
	}
}

func (r *Resource[T]) searchQuery(q orm.Query[T], dialect database.Dialect, s string) orm.Query[T] {
	op := "LIKE" // SQLite and MySQL compare case-insensitively by default
	if dialect.Name() == "postgres" {
		op = "ILIKE"
	}

	// "!" escapes the wildcards so the text matches literally. Unlike "\",
	// it is not special in MySQL string literals, and it replaces Postgres'
	// default "\" escape.
	pattern := "%" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s) + "%"

	conds := make([]string, len(r.search))
	args := make([]any, len(r.search))

	for i, col := range r.search {
		conds[i] = dialect.Quote(col) + " " + op + " ? ESCAPE '!'"
		args[i] = pattern
	}

	return q.WhereRaw(strings.Join(conds, " OR "), args...)
}

func (r *Resource[T]) filterView(f filter, qs url.Values) (filterView, any, bool) {
	col := r.byName[f.column]
	fv := filterView{Label: col.label, Param: "filter[" + f.column + "]"}

	opts := f.options
	labels := map[string]string{}

	if len(opts) == 0 && col.typ.Kind() == reflect.Bool {
		opts = []string{"true", "false"}
		labels = map[string]string{"true": "Yes", "false": "No"}
	}

	raw := qs.Get(fv.Param)

	val, ok := filterValue(col.typ, raw)
	if raw == "" || !ok || (len(opts) > 0 && !slices.Contains(opts, raw)) {
		raw, ok = "", false
	}

	fv.Value = raw

	for _, o := range opts {
		label := labels[o]
		if label == "" {
			label = humanize(o)
		}

		fv.Options = append(fv.Options, option{Value: o, Label: label, Selected: o == raw})
	}

	return fv, val, ok
}

func (r *Resource[T]) headers(base string, qs url.Values, lv *listView) []header {
	var hs []header

	for _, col := range r.list {
		h := header{Label: r.byName[col].label}

		if slices.Contains(r.sortable, col) {
			dir := "asc"
			if lv.Sort == col {
				h.Sorted = lv.Dir
				if lv.Dir == "asc" {
					dir = "desc"
				}
			}

			h.SortURL = withQuery(base, qs, map[string]string{"sort": col, "dir": dir, "page": ""})
		}

		hs = append(hs, h)
	}

	for _, cc := range r.computed {
		hs = append(hs, header{Label: cc.label})
	}

	return hs
}

func (r *Resource[T]) row(c *web.Ctx, p *Panel, rec *T) row {
	rv := reflect.ValueOf(rec).Elem()
	id := r.id(rv)
	out := row{ID: id}

	for _, col := range r.list {
		out.Cells = append(out.Cells, truncate(display(rv.FieldByIndex(r.byName[col].index)), listMaxRune))
	}

	for _, cc := range r.computed {
		out.Cells = append(out.Cells, cc.fn(*rec))
	}

	esc := url.PathEscape(id)

	if p.can(c, AbilityView, rec) {
		out.ShowURL = p.url(r.slug, esc)
	}

	if p.can(c, AbilityUpdate, rec) {
		out.EditURL = p.url(r.slug, esc, "edit")
	}

	if p.can(c, AbilityDelete, rec) {
		out.DelURL = p.url(r.slug, esc, "delete")
	}

	return out
}

func newPager(base string, qs url.Values, page, last int) pager {
	pg := pager{Page: page, Last: last}

	if page > 1 {
		pg.PrevURL = withQuery(base, qs, map[string]string{"page": strconv.Itoa(page - 1)})
	}

	if page < last {
		pg.NextURL = withQuery(base, qs, map[string]string{"page": strconv.Itoa(page + 1)})
	}

	return pg
}

// withQuery returns base with qs, where set overrides keys ("" removes).
func withQuery(base string, qs url.Values, set map[string]string) string {
	out := url.Values{}
	maps.Copy(out, qs)

	for k, v := range set {
		if v == "" {
			out.Del(k)
		} else {
			out.Set(k, v)
		}
	}

	if len(out) == 0 {
		return base
	}

	return base + "?" + out.Encode()
}

func (r *Resource[T]) show(p *Panel) web.HandlerFunc {
	return func(c *web.Ctx) error {
		rec, err := r.find(c, p)
		if err != nil {
			return err
		}

		if err := p.authorize(c, AbilityView, rec); err != nil {
			return err
		}

		rv := reflect.ValueOf(rec).Elem()
		id := r.id(rv)
		esc := url.PathEscape(id)
		recView := &recordView{ID: id}

		for _, col := range r.columns {
			if r.hidden[col.name] || r.sensitive[col.name] {
				continue
			}

			recView.Fields = append(
				recView.Fields,
				fieldView{Label: col.label, Value: display(rv.FieldByIndex(col.index))},
			)
		}

		for _, cc := range r.computed {
			recView.Fields = append(recView.Fields, fieldView{Label: cc.label, Value: cc.fn(*rec)})
		}

		if p.can(c, AbilityUpdate, rec) {
			recView.EditURL = p.url(r.slug, esc, "edit")
		}

		if p.can(c, AbilityDelete, rec) {
			recView.DelURL = p.url(r.slug, esc, "delete")
		}

		data := p.base(c, r.singular+" "+id, r.slug)
		data.Resource, data.Record = r.view(c, p), recView

		return p.render(c, http.StatusOK, "show", "", data)
	}
}

func (r *Resource[T]) create(p *Panel) web.HandlerFunc {
	return func(c *web.Ctx) error {
		if err := p.authorize(c, AbilityCreate, r.subject()); err != nil {
			return err
		}

		return r.renderForm(c, p, http.StatusOK, nil, nil, nil, nil)
	}
}

func (r *Resource[T]) edit(p *Panel) web.HandlerFunc {
	return func(c *web.Ctx) error {
		rec, err := r.find(c, p)
		if err != nil {
			return err
		}

		if err := p.authorize(c, AbilityUpdate, rec); err != nil {
			return err
		}

		return r.renderForm(c, p, http.StatusOK, rec, nil, nil, nil)
	}
}

// renderForm renders the create (rec == nil) or edit form, with old input
// and field errors after a failed submission.
func (r *Resource[T]) renderForm(c *web.Ctx, p *Panel, status int, rec *T, old map[string]string,
	fieldErrs map[string][]string, general []string,
) error {
	fv := &formView{Action: p.url(r.slug), Cancel: p.url(r.slug), Submit: "Create " + r.singular, Errors: general}
	title := "New " + r.singular

	var rv reflect.Value

	if rec != nil {
		rv = reflect.ValueOf(rec).Elem()
		id := url.PathEscape(r.id(rv))
		fv.Action, fv.Cancel, fv.Submit = p.url(r.slug, id), p.url(r.slug, id), "Save"
		title = "Edit " + r.singular + " " + r.id(rv)
	}

	shown := map[string]bool{}

	for i := range r.inputs {
		in := &r.inputs[i]
		if r.hidden[in.column] || (rec == nil && r.readonly[in.column]) {
			continue
		}

		shown[in.key] = true
		fv.Fields = append(fv.Fields, r.formField(in, rv, old, fieldErrs))
	}

	if rec != nil {
		for _, col := range r.columns {
			if r.readonly[col.name] && !r.hidden[col.name] && !r.sensitive[col.name] && !r.isInput(col.name) {
				fv.Fields = append(fv.Fields, formField{
					Key: col.name, ID: "f-" + col.name, Label: col.label, Type: InputText, ReadOnly: true,
					Value: display(rv.FieldByIndex(col.index)),
				})
			}
		}
	}

	for _, key := range sortedKeys(fieldErrs) {
		if !shown[key] {
			fv.Errors = append(fv.Errors, fieldErrs[key]...)
		}
	}

	data := p.base(c, title, r.slug)
	data.Resource, data.Form = r.view(c, p), fv

	return p.render(c, status, "form", "", data)
}

func (r *Resource[T]) isInput(col string) bool {
	for i := range r.inputs {
		if r.inputs[i].column == col {
			return true
		}
	}

	return false
}

func (r *Resource[T]) formField(
	in *input,
	rec reflect.Value,
	old map[string]string,
	fieldErrs map[string][]string,
) formField {
	f := formField{
		Key: in.key, ID: "f-" + in.key, Label: in.label, Type: in.kind, Options: in.enum,
		ReadOnly: r.readonly[in.column], Required: in.req,
	}

	sensitive := r.sensitive[in.column]

	switch {
	case sensitive:
		// Secrets are never echoed back into the page.
		f.Required = in.req && !rec.IsValid()
	case old != nil && !f.ReadOnly:
		f.Value = old[in.key]
	case rec.IsValid():
		f.Value = formValue(rec.FieldByIndex(r.byName[in.column].index))
	}

	if in.kind == InputCheckbox {
		f.Checked = f.Value == "true"
	}

	if msgs := fieldErrs[in.key]; len(msgs) > 0 {
		f.Error = msgs[0]
	}

	if n, ok := in.attrs["maxLength"].(int); ok {
		f.MaxLength = strconv.Itoa(n)
	}

	if v, ok := in.attrs["minimum"]; ok {
		f.Min = fmt.Sprint(v)
	}

	if v, ok := in.attrs["maximum"]; ok {
		f.Max = fmt.Sprint(v)
	}

	return f
}

// collect reads the submitted form: only schema keys are read, hidden and
// read-only keys are never taken from the request, and a blank sensitive
// field keeps the stored secret on edit.
func (r *Resource[T]) collect(c *web.Ctx, rec *T) (in map[string]any, old map[string]string, err error) {
	if _, err := c.Input(); err != nil {
		return nil, nil, err
	}

	form := c.Request().PostForm
	in, old = map[string]any{}, map[string]string{}

	for i := range r.inputs {
		inp := &r.inputs[i]
		switch {
		case r.hidden[inp.column]:
			continue
		case r.readonly[inp.column]:
			if rec != nil {
				in[inp.key] = formValue(reflect.ValueOf(rec).Elem().FieldByIndex(r.byName[inp.column].index))
			}

			continue
		}

		vals := form[inp.key]
		if len(vals) == 0 {
			continue
		}

		v := vals[len(vals)-1] // checkboxes send a hidden "false" first

		if r.sensitive[inp.column] {
			if v == "" && rec != nil {
				continue
			}
		} else {
			old[inp.key] = v
		}

		in[inp.key] = v
	}

	return in, old, nil
}

// apply writes the parsed schema fields that were submitted into dst.
func (r *Resource[T]) apply(dst, parsed reflect.Value, submitted map[string]any) {
	for i := range r.inputs {
		inp := &r.inputs[i]
		if _, ok := submitted[inp.key]; !ok || r.readonly[inp.column] || r.hidden[inp.column] {
			continue
		}

		inp.assign(dst.FieldByIndex(r.byName[inp.column].index), parsed.FieldByIndex(inp.src))
	}
}

// formErrors turns validation or conflict errors into form errors; other
// errors are returned as they are.
func formErrors(err error) (map[string][]string, []string, bool) {
	if issues, ok := z.AsIssues(err); ok {
		return issues.Fields(), nil, true
	}

	if errs.Status(err) == http.StatusConflict {
		return nil, []string{"This record conflicts with an existing one."}, true
	}

	return nil, nil, false
}

func (r *Resource[T]) save(ctx context.Context, p *Panel, rec *T, action string, before map[string]any) error {
	rv := reflect.ValueOf(rec).Elem()
	cols := r.auditColumns()

	return p.cfg.DB.Tx(ctx, func(tx *database.Tx) error {
		for _, fn := range r.beforeSave {
			if err := fn(ctx, rec); err != nil {
				return err
			}
		}

		var err error
		if action == ActionCreate {
			err = orm.Create(ctx, tx, rec)
		} else {
			err = orm.Update(ctx, tx, rec)
		}

		if err != nil {
			return err
		}

		changes := r.diff(before, r.snapshot(rv, cols), cols)
		if len(changes) == 0 && action == ActionUpdate {
			return nil
		}

		return writeAudit(ctx, tx, &AuditLog{
			ActorID: p.actor(ctx), Resource: r.slug, RecordID: r.id(rv), Action: action,
			Changes: orm.JSON[map[string]Change]{V: changes},
		})
	})
}

func (r *Resource[T]) store(p *Panel) web.HandlerFunc {
	return func(c *web.Ctx) error {
		if err := p.authorize(c, AbilityCreate, r.subject()); err != nil {
			return err
		}

		in, old, err := r.collect(c, nil)
		if err != nil {
			return err
		}

		rec := new(T)

		err = r.parseInto(rec, in)
		if err == nil {
			err = r.save(c.Context(), p, rec, ActionCreate, map[string]any{})
		}

		if err != nil {
			if fe, general, ok := formErrors(err); ok {
				return r.renderForm(c, p, http.StatusUnprocessableEntity, nil, old, fe, general)
			}

			return err
		}

		_ = c.Flash(FlashKey, r.singular+" created.")

		return c.Redirect(r.after(c, p, rec))
	}
}

func (r *Resource[T]) parseInto(rec *T, in map[string]any) error {
	parsed, err := r.parse(in)
	if err != nil {
		return err
	}

	r.apply(reflect.ValueOf(rec).Elem(), parsed, in)

	return nil
}

// after is where a successful write redirects: the record when the admin
// may view it, else the list.
func (r *Resource[T]) after(c *web.Ctx, p *Panel, rec *T) string {
	if p.can(c, AbilityView, rec) {
		return p.url(r.slug, url.PathEscape(r.id(reflect.ValueOf(rec).Elem())))
	}

	return p.url(r.slug)
}

func (r *Resource[T]) update(p *Panel) web.HandlerFunc {
	return func(c *web.Ctx) error {
		rec, err := r.find(c, p)
		if err != nil {
			return err
		}

		if err := p.authorize(c, AbilityUpdate, rec); err != nil {
			return err
		}

		in, old, err := r.collect(c, rec)
		if err != nil {
			return err
		}

		before := r.snapshot(reflect.ValueOf(rec).Elem(), r.auditColumns())

		err = r.parseInto(rec, in)
		if err == nil {
			err = r.save(c.Context(), p, rec, ActionUpdate, before)
		}

		if err != nil {
			if fe, general, ok := formErrors(err); ok {
				return r.renderForm(c, p, http.StatusUnprocessableEntity, rec, old, fe, general)
			}

			return err
		}

		_ = c.Flash(FlashKey, r.singular+" saved.")

		return c.Redirect(r.after(c, p, rec))
	}
}

func (r *Resource[T]) confirmDelete(p *Panel) web.HandlerFunc {
	return func(c *web.Ctx) error {
		rec, err := r.find(c, p)
		if err != nil {
			return err
		}

		if err := p.authorize(c, AbilityDelete, rec); err != nil {
			return err
		}

		id := r.id(reflect.ValueOf(rec).Elem())
		esc := url.PathEscape(id)

		data := p.base(c, "Delete "+r.singular+" "+id, r.slug)
		data.Resource = r.view(c, p)
		data.Record = &recordView{ID: id, DeleteAction: p.url(r.slug, esc, "delete"), Cancel: p.url(r.slug)}

		return p.render(c, http.StatusOK, "delete", "", data)
	}
}

func (r *Resource[T]) destroy(p *Panel) web.HandlerFunc {
	return func(c *web.Ctx) error {
		rec, err := r.find(c, p)
		if err != nil {
			return err
		}

		if err := p.authorize(c, AbilityDelete, rec); err != nil {
			return err
		}

		ctx := c.Context()
		rv := reflect.ValueOf(rec).Elem()
		cols := r.auditColumns()

		err = p.cfg.DB.Tx(ctx, func(tx *database.Tx) error {
			if err := orm.Delete(ctx, tx, rec); err != nil {
				return err
			}

			return writeAudit(ctx, tx, &AuditLog{
				ActorID: p.actor(ctx), Resource: r.slug, RecordID: r.id(rv), Action: ActionDelete,
				Changes: orm.JSON[map[string]Change]{V: r.diff(r.snapshot(rv, cols), map[string]any{}, cols)},
			})
		})
		if err != nil {
			if errors.Is(err, orm.ErrNotFound) {
				return orm.ErrNotFound
			}

			return err
		}

		_ = c.Flash(FlashKey, r.singular+" deleted.")

		return c.Redirect(p.url(r.slug))
	}
}

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
