package orm

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
)

// maxParams is the bound-parameter limit of one statement: SQLite 3.32+
// allows 32766, PostgreSQL's wire protocol and MySQL's prepared statements
// 65535.
func maxParams(d database.Dialect) int {
	switch d.Name() {
	case "sqlite":
		return 32766
	case "postgres", "mysql":
		return 65535
	default:
		return 999 // the historical SQLite limit, safe everywhere
	}
}

// CreateMany inserts models with multi-row INSERT statements, split into
// batches that stay under the dialect's bound-parameter limit. It fills
// timestamps and runs BeforeCreate on every model first, then AfterCreate
// once every row is stored.
//
// On a *database.DB the batches run in one transaction, so either every
// row is stored or none is; on a *database.Tx they join it and the caller
// commits or rolls back.
//
// Auto-increment ids are written back when every model has a zero id (or
// every model sets its own; mixing both is an error). PostgreSQL and
// SQLite return them with RETURNING; MySQL cannot report the ids of a
// multi-row insert reliably, so there each row is its own INSERT.
func CreateMany[T any](ctx context.Context, ex database.Executor, models []T) error {
	return createMany(ctx, ex, models, maxParams(ex.Dialect()))
}

func createMany[T any](ctx context.Context, ex database.Executor, models []T, limit int) error {
	if len(models) == 0 {
		return nil
	}

	m := metaOf[T]()
	now := clockFrom(ctx)

	for i := range models {
		if err := hook(ctx, &models[i], BeforeCreator.BeforeCreate); err != nil {
			return err
		}

		rv := reflect.ValueOf(&models[i]).Elem()
		setTime(rv, m, "created_at", now, true)
		setTime(rv, m, "updated_at", now, true)
	}

	genID, err := generatedIDs(m, models)
	if err != nil {
		return err
	}

	var fields []field

	for _, f := range m.fields {
		if !genID || f.column != m.pk {
			fields = append(fields, f)
		}
	}

	insert := func(ex database.Executor) error {
		if genID && ex.Dialect().Name() == "mysql" {
			for i := range models {
				if err := insertOne(ctx, ex, m, reflect.ValueOf(&models[i]).Elem()); err != nil {
					return err
				}
			}

			return nil
		}

		perBatch := max(1, limit/len(fields))

		for batch := range slices.Chunk(models, perBatch) {
			if err := insertBatch(ctx, ex, m, fields, batch, genID); err != nil {
				return err
			}
		}

		return nil
	}

	if err := inTx(ctx, ex, insert); err != nil {
		if genID {
			// The rows were rolled back (or will be): do not leave ids that
			// point at nothing.
			for i := range models {
				pk := reflect.ValueOf(&models[i]).Elem().FieldByIndex(m.byColumn[m.pk].index)
				pk.SetZero()
			}
		}

		return err
	}

	for i := range models {
		if err := hook(ctx, &models[i], AfterCreator.AfterCreate); err != nil {
			return err
		}
	}

	return nil
}

// generatedIDs reports whether the database assigns the primary keys:
// true when the key auto-increments and every model leaves it zero.
func generatedIDs[T any](m *meta, models []T) (bool, error) {
	if !m.autoID {
		return false, nil
	}

	zero := 0

	for i := range models {
		if reflect.ValueOf(&models[i]).Elem().FieldByIndex(m.byColumn[m.pk].index).IsZero() {
			zero++
		}
	}

	switch zero {
	case 0:
		return false, nil
	case len(models):
		return true, nil
	default:
		return false, fmt.Errorf("orm: CreateMany %s: cannot mix models with and without ids", m.table)
	}
}

// inTx runs fn in one transaction on a *database.DB, inside the caller's on
// a *database.Tx, and directly on any other executor.
//
// AfterCreate hooks run after inTx returns: on SQLite the writer pool has
// one connection, so a hook writing through the *DB would deadlock while
// the transaction holds it.
func inTx(ctx context.Context, ex database.Executor, fn func(database.Executor) error) error {
	switch ex.(type) {
	case *database.DB, *database.Tx:
		return database.RunInTx(ctx, ex, func(tx *database.Tx) error { return fn(tx) })
	default:
		return fn(ex)
	}
}

func insertBatch[T any](
	ctx context.Context, ex database.Executor, m *meta, fields []field, batch []T, genID bool,
) error {
	d := ex.Dialect()

	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = d.Quote(f.column)
	}

	row := "(" + strings.TrimSuffix(strings.Repeat("?, ", len(fields)), ", ") + ")"
	args := make([]any, 0, len(batch)*len(fields))

	for i := range batch {
		rv := reflect.ValueOf(&batch[i]).Elem()

		for _, f := range fields {
			v, err := bindValue(ex, m.table, f.column, rv.FieldByIndex(f.index).Interface())
			if err != nil {
				return err
			}

			args = append(args, v)
		}
	}

	q := "INSERT INTO " + d.Quote(m.table) + " (" + strings.Join(cols, ", ") + ") VALUES " +
		strings.TrimSuffix(strings.Repeat(row+", ", len(batch)), ", ")

	if !genID {
		if _, err := ex.ExecContext(ctx, renumber(q, d), args...); err != nil {
			return fmt.Errorf("orm: insert %s: %w", m.table, mapError(err))
		}

		return nil
	}

	ids, err := returnIDs(ctx, ex, renumber(q+" RETURNING "+d.Quote(m.pk), d), args)
	if err != nil {
		return fmt.Errorf("orm: insert %s: %w", m.table, mapError(err))
	}

	if len(ids) != len(batch) {
		return fmt.Errorf("orm: insert %s: got %d ids for %d rows", m.table, len(ids), len(batch))
	}

	// Neither PostgreSQL nor SQLite promises RETURNING order, but both
	// assign generated keys in VALUES order, so the sorted ids line up
	// with the batch.
	slices.Sort(ids)

	for i := range batch {
		if err := setID(reflect.ValueOf(&batch[i]).Elem().FieldByIndex(m.byColumn[m.pk].index), ids[i]); err != nil {
			return fmt.Errorf("orm: insert %s: %w", m.table, err)
		}
	}

	return nil
}

func returnIDs(ctx context.Context, ex database.Executor, q string, args []any) (ids []int64, err error) {
	rows, err := ex.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}

	defer func() { err = errors.Join(err, rows.Close()) }()

	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}

	return ids, rows.Err()
}
