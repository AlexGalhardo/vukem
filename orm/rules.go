package orm

import (
	"context"
	"reflect"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
)

// Rule is a validation rule that needs the database (unique, exists). z
// schemas validate shape and format; Rules run after parsing:
//
//	in, err := web.Parse(c, requests.RegisterSchema)
//	if err != nil {
//		return err
//	}
//	if err := orm.Validate(c.Context(), db, orm.Unique[models.User]("email", in.Email)); err != nil {
//		return err // 422 (or flashed errors for forms), like any validation error
//	}
type Rule struct {
	field string
	// message replaces the built-in text when set (Message).
	message string
	// def is the built-in English text for a field name; key translates
	// it in the request locale. Message clears key.
	def    func(field string) string
	key    string
	check  func(ctx context.Context, ex database.Executor, r Rule) (bool, error)
	ignore any
	value  any
}

// Unique passes when no row of T has column = value (soft-deleted rows
// count, as the database constraint does). Use Ignore(id) when updating.
func Unique[T any](column string, value any) Rule {
	return Rule{
		field: column,
		value: value,
		key:   "validation.unique",
		def:   func(f string) string { return "The " + f + " has already been taken." },
		check: func(ctx context.Context, ex database.Executor, r Rule) (bool, error) {
			q := QueryOf[T](ex).WithTrashed().Where(column, value)
			if r.ignore != nil {
				if err := q.m.singleKey("Unique.Ignore"); err != nil {
					return false, err
				}

				q = q.Where(q.m.pk, "!=", r.ignore)
			}

			taken, err := q.Exists(ctx)

			return !taken, err
		},
	}
}

// Exists passes when a (not soft-deleted) row of T has column = value.
func Exists[T any](column string, value any) Rule {
	return Rule{
		field: column,
		value: value,
		key:   "validation.exists",
		def:   func(f string) string { return "The selected " + f + " is invalid." },
		check: func(ctx context.Context, ex database.Executor, _ Rule) (bool, error) {
			return QueryOf[T](ex).Where(column, value).Exists(ctx)
		},
	}
}

// Ignore excludes the row with primary key id (the model being updated).
func (r Rule) Ignore(id any) Rule { r.ignore = id; return r }

// Field reports the error on name instead of the column name; the
// built-in message names it too ("The contact has already been taken.").
func (r Rule) Field(name string) Rule { r.field = name; return r }

// Message replaces the error message; it is used as is, not translated.
func (r Rule) Message(msg string) Rule { r.message, r.key = msg, ""; return r }

// Validate runs rules and returns an *errs.ValidationError (422) listing
// the failing fields, or nil. Empty values (nil, "", zero numbers) are
// skipped: required-ness belongs to the z schema. The built-in messages
// follow the request locale ("validation.unique", "validation.exists"; see
// errs.WithTranslator, installed by i18n.Middleware).
func Validate(ctx context.Context, ex database.Executor, rules ...Rule) error {
	verr := errs.NewValidation()

	for _, r := range rules {
		if isEmpty(r.value) {
			continue
		}

		ok, err := r.check(ctx, ex, r)
		if err != nil {
			return err
		}

		if !ok {
			verr = verr.Add(r.field, r.localized(ctx))
		}
	}

	if verr.Empty() {
		return nil
	}

	return verr
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		return rv.IsNil() || rv.Elem().IsZero()
	}

	return rv.IsZero()
}

// localized translates the built-in message through the Translator of ctx
// (errs.WithTranslator), naming the field by its
// "validation.attributes.<field>" translation when the catalog has one.
func (r Rule) localized(ctx context.Context) string {
	if r.key == "" {
		return r.message
	}

	field := errs.Translate(ctx, "validation.attributes."+r.field, r.field, nil)

	return errs.Translate(ctx, r.key, r.def(field), map[string]any{"field": field})
}
