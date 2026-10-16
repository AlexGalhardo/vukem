package orm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
)

// Model hooks: implement them on *T to run code around writes. Returning an
// error aborts the write.
type (
	// BeforeCreater runs before INSERT.
	BeforeCreater interface {
		BeforeCreate(ctx context.Context) error
	}
	// AfterCreater runs after INSERT.
	AfterCreater interface {
		AfterCreate(ctx context.Context) error
	}
	// BeforeUpdater runs before UPDATE.
	BeforeUpdater interface {
		BeforeUpdate(ctx context.Context) error
	}
	// AfterUpdater runs after UPDATE.
	AfterUpdater interface {
		AfterUpdate(ctx context.Context) error
	}
	// BeforeDeleter runs before DELETE (or soft delete).
	BeforeDeleter interface {
		BeforeDelete(ctx context.Context) error
	}
)

// now is the clock used for timestamps; tests replace it via WithClock.
type clockKey struct{}

// WithClock makes writes in ctx use now for timestamps (tests).
func WithClock(ctx context.Context, now func() time.Time) context.Context {
	return context.WithValue(ctx, clockKey{}, now)
}

func clockFrom(ctx context.Context) time.Time {
	if f, ok := ctx.Value(clockKey{}).(func() time.Time); ok {
		return f().UTC()
	}

	return time.Now().UTC()
}

func hook[I any](ctx context.Context, v any, call func(I, context.Context) error) error {
	if h, ok := v.(I); ok {
		return call(h, ctx)
	}

	return nil
}

// Create inserts model, filling the auto-increment id and timestamps.
func Create[T any](ctx context.Context, ex database.Executor, model *T) error {
	m := metaOf[T]()

	if err := hook(ctx, model, BeforeCreater.BeforeCreate); err != nil {
		return err
	}

	rv := reflect.ValueOf(model).Elem()
	now := clockFrom(ctx)

	setTime(rv, m, "created_at", now, true)
	setTime(rv, m, "updated_at", now, true)

	var (
		cols, marks []string
		args        []any
	)

	for _, f := range m.fields {
		fv := rv.FieldByIndex(f.index)
		if f.column == m.pk && m.autoID && fv.IsZero() {
			continue
		}

		cols = append(cols, ex.Dialect().Quote(f.column))
		marks = append(marks, "?")
		args = append(args, fv.Interface())
	}

	q := "INSERT INTO " + ex.Dialect().Quote(m.table) + " (" + strings.Join(cols, ", ") + ") VALUES (" +
		strings.Join(marks, ", ") + ")"

	pk := rv.FieldByIndex(m.byColumn[m.pk].index)

	if m.autoID && pk.IsZero() {
		q += " RETURNING " + ex.Dialect().Quote(m.pk)
		if err := ex.QueryRowContext(ctx, renumber(q, ex.Dialect()), args...).Scan(pk.Addr().Interface()); err != nil {
			return fmt.Errorf("orm: insert %s: %w", m.table, mapError(err))
		}
	} else if _, err := ex.ExecContext(ctx, renumber(q, ex.Dialect()), args...); err != nil {
		return fmt.Errorf("orm: insert %s: %w", m.table, mapError(err))
	}

	return hook(ctx, model, AfterCreater.AfterCreate)
}

// Update writes every column of model (except the primary key and
// created_at) and refreshes updated_at.
func Update[T any](ctx context.Context, ex database.Executor, model *T) error {
	m := metaOf[T]()

	if err := hook(ctx, model, BeforeUpdater.BeforeUpdate); err != nil {
		return err
	}

	rv := reflect.ValueOf(model).Elem()
	setTime(rv, m, "updated_at", clockFrom(ctx), false)

	var (
		sets []string
		args []any
	)

	for _, f := range m.fields {
		if f.column == m.pk || f.column == "created_at" {
			continue
		}

		sets = append(sets, ex.Dialect().Quote(f.column)+" = ?")
		args = append(args, rv.FieldByIndex(f.index).Interface())
	}

	args = append(args, rv.FieldByIndex(m.byColumn[m.pk].index).Interface())

	q := "UPDATE " + ex.Dialect().Quote(m.table) + " SET " + strings.Join(sets, ", ") +
		" WHERE " + ex.Dialect().Quote(m.pk) + " = ?"

	res, err := ex.ExecContext(ctx, renumber(q, ex.Dialect()), args...)
	if err != nil {
		return fmt.Errorf("orm: update %s: %w", m.table, mapError(err))
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}

	return hook(ctx, model, AfterUpdater.AfterUpdate)
}

// Delete removes model, or sets deleted_at when the model embeds
// SoftDeletes.
func Delete[T any](ctx context.Context, ex database.Executor, model *T) error {
	m := metaOf[T]()

	if err := hook(ctx, model, BeforeDeleter.BeforeDelete); err != nil {
		return err
	}

	rv := reflect.ValueOf(model).Elem()
	id := rv.FieldByIndex(m.byColumn[m.pk].index).Interface()

	if m.soft {
		now := clockFrom(ctx)

		n, err := QueryOf[T](ex).Where(m.pk, id).Update(ctx, Set{"deleted_at": now})
		if err != nil {
			return err
		}

		if n == 0 {
			return ErrNotFound
		}

		rv.FieldByIndex(m.byColumn["deleted_at"].index).Set(reflect.ValueOf(sql.Null[time.Time]{V: now, Valid: true}))

		return nil
	}

	return ForceDelete(ctx, ex, model)
}

// ForceDelete removes model even when it uses soft deletes.
func ForceDelete[T any](ctx context.Context, ex database.Executor, model *T) error {
	m := metaOf[T]()
	id := reflect.ValueOf(model).Elem().FieldByIndex(m.byColumn[m.pk].index).Interface()

	n, err := QueryOf[T](ex).WithTrashed().Where(m.pk, id).Delete(ctx)
	if err != nil {
		return err
	}

	if n == 0 {
		return ErrNotFound
	}

	return nil
}

// Restore clears deleted_at on a soft-deleted model.
func Restore[T any](ctx context.Context, ex database.Executor, model *T) error {
	m := metaOf[T]()
	if !m.soft {
		return fmt.Errorf("orm: %s does not use soft deletes", m.table)
	}

	rv := reflect.ValueOf(model).Elem()
	id := rv.FieldByIndex(m.byColumn[m.pk].index).Interface()

	if _, err := QueryOf[T](ex).OnlyTrashed().Where(m.pk, id).Update(ctx, Set{"deleted_at": nil}); err != nil {
		return err
	}

	rv.FieldByIndex(m.byColumn["deleted_at"].index).Set(reflect.Zero(reflect.TypeFor[sql.Null[time.Time]]()))

	return nil
}

// Set is a column → value map for bulk updates.
type Set map[string]any

// Update sets columns on every matching row and returns the count.
// updated_at is refreshed unless set explicitly.
func (q Query[T]) Update(ctx context.Context, set Set) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}

	if len(set) == 0 {
		return 0, errors.New("orm: Update needs at least one column")
	}

	if q.m.hasUpdated {
		if _, ok := set["updated_at"]; !ok {
			set = mapsClone(set)
			set["updated_at"] = clockFrom(ctx)
		}
	}

	keys := make([]string, 0, len(set))
	for k := range set {
		if _, err := q.m.column(k); err != nil {
			return 0, err
		}

		keys = append(keys, k)
	}

	slices.Sort(keys)

	sets := make([]string, len(keys))
	args := make([]any, 0, len(keys))

	for i, k := range keys {
		sets[i] = q.quote(k) + " = ?"
		args = append(args, set[k])
	}

	where, wargs := q.whereSQL()
	query := renumber("UPDATE "+q.quote(q.m.table)+" SET "+strings.Join(sets, ", ")+where, q.ex.Dialect())

	res, err := q.ex.ExecContext(ctx, query, append(args, wargs...)...)
	if err != nil {
		return 0, fmt.Errorf("orm: update %s: %w", q.m.table, mapError(err))
	}

	return res.RowsAffected()
}

// Delete removes every matching row (soft-deleting for SoftDeletes models
// unless WithTrashed/OnlyTrashed was used, which deletes for real) and
// returns the count.
func (q Query[T]) Delete(ctx context.Context) (int64, error) {
	if q.err != nil {
		return 0, q.err
	}

	if q.m.soft && q.trashed == 0 {
		return q.Update(ctx, Set{"deleted_at": clockFrom(ctx)})
	}

	where, args := q.whereSQL()

	res, err := q.ex.ExecContext(ctx, renumber("DELETE FROM "+q.quote(q.m.table)+where, q.ex.Dialect()), args...)
	if err != nil {
		return 0, fmt.Errorf("orm: delete %s: %w", q.m.table, mapError(err))
	}

	return res.RowsAffected()
}

func mapsClone(s Set) Set {
	out := make(Set, len(s)+1)
	maps.Copy(out, s)

	return out
}

func setTime(rv reflect.Value, m *meta, col string, t time.Time, onlyZero bool) {
	f, ok := m.byColumn[col]
	if !ok || f.typ != reflect.TypeFor[time.Time]() {
		return
	}

	fv := rv.FieldByIndex(f.index)
	if onlyZero && !fv.IsZero() {
		return
	}

	fv.Set(reflect.ValueOf(t))
}

// ErrConflict is returned (wrapped) when a write violates a unique
// constraint; it reports HTTP status 409 so handlers answer Conflict.
var ErrConflict error = &conflictError{}

type conflictError struct{ cause error }

func (e *conflictError) Error() string {
	if e.cause != nil {
		return "unique constraint violated: " + e.cause.Error()
	}

	return "unique constraint violated"
}

func (e *conflictError) Unwrap() error { return e.cause }

// HTTPStatus implements errs.StatusCoder.
func (*conflictError) HTTPStatus() int { return 409 }

func (*conflictError) Is(target error) bool { _, ok := target.(*conflictError); return ok }

// mapError converts driver-specific unique violations into ErrConflict.
func mapError(err error) error {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unique constraint") || strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "constraint failed: unique") {
		return &conflictError{cause: err}
	}

	return err
}

// JSON stores T as JSON text in one column.
type JSON[T any] struct {
	V T
}

// Value implements driver.Valuer.
func (j JSON[T]) Value() (driver.Value, error) {
	b, err := json.Marshal(j.V)
	if err != nil {
		return nil, err
	}

	return string(b), nil
}

// Scan implements sql.Scanner.
func (j *JSON[T]) Scan(src any) error {
	switch s := src.(type) {
	case nil:
		var zero T
		j.V = zero

		return nil
	case string:
		return json.Unmarshal([]byte(s), &j.V)
	case []byte:
		return json.Unmarshal(s, &j.V)
	default:
		return fmt.Errorf("orm: cannot scan %T into JSON", src)
	}
}

// MarshalJSON encodes the inner value.
func (j JSON[T]) MarshalJSON() ([]byte, error) { return json.Marshal(j.V) }

// UnmarshalJSON decodes into the inner value.
func (j *JSON[T]) UnmarshalJSON(b []byte) error { return json.Unmarshal(b, &j.V) }
