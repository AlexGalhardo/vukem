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
// Atomicity holds only for *database.DB and *database.Tx: on a DB the
// batches run in one transaction, so either every row is stored or none
// is; on a Tx they join it and the caller commits or rolls back. Any other
// executor (a wrapper) runs the statements directly, so batches written
// before a failure stay written and keep their ids.
//
// AfterCreate runs after that transaction commits (a hook writing through
// the DB would otherwise deadlock on SQLite's single writer), so on a DB an
// AfterCreate error means the rows are stored and the caller still gets
// the error.
//
// Auto-increment ids are written back when every model has a zero id (or
// every model sets its own; mixing both is an error). PostgreSQL and
// SQLite return them with RETURNING, in VALUES order; the ids must be
// strictly increasing (an ascending sequence), otherwise CreateMany fails
// instead of guessing which row got which id. MySQL cannot report the ids
// of a multi-row insert reliably, so there each row is its own INSERT. A
// model with no column besides its generated key is rejected.
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

	if len(fields) == 0 {
		return fmt.Errorf("orm: CreateMany %s: no columns to insert besides the generated key", m.table)
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
		// Only a transaction started here is surely rolled back; elsewhere
		// written rows may persist, so their ids stay.
		if _, own := ex.(*database.DB); own && genID {
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

	// RETURNING follows VALUES order in practice on PostgreSQL and SQLite,
	// but neither promises it; strictly increasing ids from an ascending
	// sequence confirm the pairing, anything else fails closed.
	if err := checkIncreasing(ids); err != nil {
		return fmt.Errorf("orm: insert %s: %w", m.table, err)
	}

	for i := range batch {
		if err := setID(reflect.ValueOf(&batch[i]).Elem().FieldByIndex(m.byColumn[m.pk].index), ids[i]); err != nil {
			return fmt.Errorf("orm: insert %s: %w", m.table, err)
		}
	}

	return nil
}

func checkIncreasing(ids []int64) error {
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			return fmt.Errorf(
				"generated ids %d then %d are not increasing; cannot match them to rows",
				ids[i-1],
				ids[i],
			)
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
