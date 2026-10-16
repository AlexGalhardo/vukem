package orm

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/AlexGalhardo/vukem/database"
)

// pivotOf returns the many-to-many relation name of T and model's key.
func pivotOf[T any](model *T, name string) (relation, any, error) {
	m := metaOf[T]()

	rel, ok := m.relations[name]
	if !ok || rel.kind != relManyToMany {
		return relation{}, nil, fmt.Errorf("orm: %s has no many-to-many relation %q", m.typ.Name(), name)
	}

	key, ok := keyOf(reflect.ValueOf(model).Elem().FieldByIndex(m.byColumn[m.pk].index))
	if !ok {
		return relation{}, nil, fmt.Errorf("orm: %s is not saved (empty primary key)", m.typ.Name())
	}

	return rel, key, nil
}

// Attach links model to the related ids through the pivot table of the
// many-to-many relation (field name). Ids already attached are skipped.
func Attach[T any](ctx context.Context, ex database.Executor, model *T, relation string, ids ...any) error {
	rel, key, err := pivotOf(model, relation)
	if err != nil {
		return err
	}

	existing, err := pivotIDs(ctx, ex, rel, key)
	if err != nil {
		return err
	}

	d := ex.Dialect()
	q := renumber("INSERT INTO "+d.Quote(rel.pivot)+" ("+d.Quote(rel.fk)+", "+d.Quote(rel.rk)+") VALUES (?, ?)", d)

	for _, id := range ids {
		if slices.Contains(existing, normKey(id)) {
			continue
		}

		if _, err := ex.ExecContext(ctx, q, key, id); err != nil {
			return fmt.Errorf("orm: attach %s: %w", rel.pivot, mapError(err))
		}

		existing = append(existing, normKey(id))
	}

	return nil
}

// Detach unlinks the related ids, or every related row when ids is empty.
func Detach[T any](ctx context.Context, ex database.Executor, model *T, relation string, ids ...any) error {
	rel, key, err := pivotOf(model, relation)
	if err != nil {
		return err
	}

	d := ex.Dialect()
	q := "DELETE FROM " + d.Quote(rel.pivot) + " WHERE " + d.Quote(rel.fk) + " = ?"
	args := []any{key}

	if len(ids) > 0 {
		q += " AND " + d.Quote(rel.rk) + " IN (" + marks(len(ids)) + ")"
		args = append(args, ids...)
	}

	if _, err := ex.ExecContext(ctx, renumber(q, d), args...); err != nil {
		return fmt.Errorf("orm: detach %s: %w", rel.pivot, err)
	}

	return nil
}

// Sync makes ids the exact set of related rows (in a transaction):
// missing ids are attached, others detached.
func Sync[T any](ctx context.Context, ex database.Executor, model *T, relation string, ids ...any) error {
	rel, key, err := pivotOf(model, relation)
	if err != nil {
		return err
	}

	want := make([]any, len(ids))
	for i, id := range ids {
		want[i] = normKey(id)
	}

	return database.RunInTx(ctx, ex, func(tx *database.Tx) error {
		have, err := pivotIDs(ctx, tx, rel, key)
		if err != nil {
			return err
		}

		var remove []any

		for _, h := range have {
			if !slices.Contains(want, h) {
				remove = append(remove, h)
			}
		}

		if len(remove) > 0 {
			if err := Detach(ctx, tx, model, relation, remove...); err != nil {
				return err
			}
		}

		return Attach(ctx, tx, model, relation, ids...)
	})
}

func pivotIDs(ctx context.Context, ex database.Executor, rel relation, key any) ([]any, error) {
	d := ex.Dialect()

	rows, err := ex.QueryContext(ctx, renumber("SELECT "+d.Quote(rel.rk)+" FROM "+d.Quote(rel.pivot)+
		" WHERE "+d.Quote(rel.fk)+" = ?", d), key)
	if err != nil {
		return nil, fmt.Errorf("orm: read %s: %w", rel.pivot, err)
	}
	defer rows.Close()

	var ids []any

	for rows.Next() {
		var id any
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}

		ids = append(ids, normKey(id))
	}

	return ids, rows.Err()
}
