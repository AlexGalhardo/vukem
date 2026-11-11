package orm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/AlexGalhardo/vukem/database"
)

// OnConflict says which unique columns identify an existing row and what to
// do when it exists.
type OnConflict struct {
	// Columns are the unique or primary key columns that identify the row.
	// PostgreSQL and SQLite use them as the conflict target. MySQL and
	// MariaDB have no target: any unique index of the table triggers the
	// update, and Columns only tell Upsert how to read back a generated id.
	Columns []string
	// Update lists the columns to overwrite with the new values. Empty (and
	// DoNothing false) means every writable column except the key,
	// created_at and Columns. updated_at is always refreshed.
	Update []string
	// DoNothing keeps the existing row untouched.
	DoNothing bool
}

// upsertPlan validates on against the model: the conflict columns and the
// columns to update.
func upsertPlan(m *meta, on OnConflict) (conflict []string, updates []field, err error) {
	if len(on.Columns) == 0 {
		return nil, nil, errors.New("orm: Upsert needs at least one conflict column")
	}

	for _, c := range on.Columns {
		col, err := m.column(c)
		if err != nil || strings.Contains(col, ".") || col == "*" {
			return nil, nil, fmt.Errorf("orm: Upsert: unknown conflict column %q on %s", c, m.table)
		}

		if f := m.byColumn[col]; reflect.PointerTo(f.typ).Implements(reflect.TypeFor[opener]()) {
			return nil, nil, fmt.Errorf("orm: Upsert: encrypted column %q cannot be a conflict column", c)
		}

		conflict = append(conflict, col)
	}

	if on.DoNothing {
		return conflict, nil, nil
	}

	if len(on.Update) == 0 {
		for _, f := range m.fields {
			if writableOnUpdate(m, f) && !slices.Contains(conflict, f.column) && f.column != "deleted_at" {
				updates = append(updates, f)
			}
		}

		return conflict, updates, nil
	}

	for _, c := range on.Update {
		col, err := m.column(c)
		if err != nil || strings.Contains(col, ".") || col == "*" {
			return nil, nil, fmt.Errorf("orm: Upsert: unknown column %q on %s", c, m.table)
		}

		f := m.byColumn[col]
		if !writableOnUpdate(m, f) || f.column == "deleted_at" {
			return nil, nil, fmt.Errorf("orm: Upsert cannot update %q on %s", c, m.table)
		}

		if !slices.ContainsFunc(updates, func(g field) bool { return g.column == f.column }) {
			updates = append(updates, f)
		}
	}

	// updated_at is refreshed on every write.
	if f, ok := m.byColumn["updated_at"]; ok &&
		!slices.ContainsFunc(updates, func(g field) bool { return g.column == f.column }) {
		updates = append(updates, f)
	}

	return conflict, updates, nil
}

// conflictSQL is the clause after VALUES for the dialect's family. A model
// with soft deletes never has a trashed row updated: PostgreSQL and SQLite
// add WHERE table.deleted_at IS NULL to the update, MySQL keeps each column
// as it was unless the row is live. keyID asks MySQL to report the id of an
// existing row through LAST_INSERT_ID(id). VALUES() is kept on purpose: it
// is the form MariaDB understands (it has no "AS new" row alias).
func conflictSQL(d database.Dialect, m *meta, conflict []string, updates []field, keyID bool) string {
	if database.MustCapabilities(d).Family == "mysql" {
		var sets []string

		if keyID {
			sets = append(sets, d.Quote(m.pk)+" = LAST_INSERT_ID("+d.Quote(m.pk)+")")
		}

		live := d.Quote(m.table+".deleted_at") + " IS NULL"

		for _, f := range updates {
			col := d.Quote(f.column)
			if m.soft {
				sets = append(sets, col+" = IF("+live+", VALUES("+col+"), "+col+")")
			} else {
				sets = append(sets, col+" = VALUES("+col+")")
			}
		}

		if len(sets) == 0 {
			// A no-op update: INSERT IGNORE would also hide other errors.
			col := d.Quote(conflict[0])
			sets = append(sets, col+" = "+col)
		}

		return " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
	}

	cols := make([]string, len(conflict))
	for i, c := range conflict {
		cols[i] = d.Quote(c)
	}

	out := " ON CONFLICT (" + strings.Join(cols, ", ") + ")"

	if len(updates) == 0 {
		return out + " DO NOTHING"
	}

	sets := make([]string, len(updates))
	for i, f := range updates {
		sets[i] = d.Quote(f.column) + " = excluded." + d.Quote(f.column)
	}

	out += " DO UPDATE SET " + strings.Join(sets, ", ")
	if m.soft {
		out += " WHERE " + d.Quote(m.table+".deleted_at") + " IS NULL"
	}

	return out
}

// errTrashed is what Upsert returns for a conflict with a trashed row. It is
// an ErrConflict (409): the row exists, and Upsert never revives it.
var errTrashed = &conflictError{cause: errors.New("row is trashed; Restore it first")}

// checkTrashed returns errTrashed when a row identified by the conflict
// columns of rows exists in the trash. Models that carry deleted_at
// themselves are skipped: they insert a trashed row on purpose.
func checkTrashed(ctx context.Context, ex database.Executor, m *meta, conflict []string, rows []reflect.Value) error {
	if !m.soft {
		return nil
	}

	d := ex.Dialect()
	deleted := m.byColumn["deleted_at"].index

	var live []reflect.Value

	for _, rv := range rows {
		if rv.FieldByIndex(deleted).IsZero() {
			live = append(live, rv)
		}
	}

	perRow := len(conflict)

	for batch := range slices.Chunk(live, max(1, maxParams(d)/perRow)) {
		parts := make([]string, len(batch))
		args := make([]any, 0, len(batch)*perRow)

		for i, rv := range batch {
			eq := make([]string, perRow)

			for k, c := range conflict {
				eq[k] = d.Quote(c) + " = ?"

				v, err := bindValue(ex, m.table, c, rv.FieldByIndex(m.byColumn[c].index).Interface())
				if err != nil {
					return err
				}

				args = append(args, v)
			}

			parts[i] = "(" + strings.Join(eq, " AND ") + ")"
		}

		q := "SELECT 1 FROM " + d.Quote(m.table) + " WHERE " + d.Quote("deleted_at") + " IS NOT NULL AND (" +
			strings.Join(parts, " OR ") + ") LIMIT 1"

		var one int

		err := queryRowWrite(ctx, ex, rebind(q, d), args...).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}

		if err != nil {
			return fmt.Errorf("orm: upsert %s: %w", m.table, err)
		}

		return fmt.Errorf("orm: upsert %s: %w", m.table, errTrashed)
	}

	return nil
}

// Upsert inserts model, or updates the row that already holds the same
// values in on.Columns, in one statement (INSERT ... ON CONFLICT on
// PostgreSQL and SQLite, ON DUPLICATE KEY UPDATE on MySQL and MariaDB).
// Timestamps are filled like Create does and updated_at is refreshed on an
// update. A generated key is written back: the id of the inserted or of the
// existing row; with DoNothing and an existing row it stays zero. A unique
// violation on another index is orm.ErrConflict.
//
// Upsert runs no hooks (BeforeCreate, AfterCreate, BeforeUpdate, AfterUpdate):
// the database decides between insert and update, so none of them would be
// right. A model with SoftDeletes is never revived or changed through a
// trashed row: that is an ErrConflict ("row is trashed; Restore it first"),
// and deleted_at is never among the updated columns. On MySQL the generated
// key of an existing row comes from LAST_INSERT_ID(id) in the update clause.
func Upsert[T any](ctx context.Context, ex database.Executor, model *T, on OnConflict) error {
	if err := checkDialect(ex); err != nil {
		return err
	}

	m := metaOf[T]()

	conflict, updates, err := upsertPlan(m, on)
	if err != nil {
		return err
	}

	rv := reflect.ValueOf(model).Elem()
	now := clockFrom(ctx)

	setTime(rv, m, "created_at", now, true)
	setTime(rv, m, "updated_at", now, true)

	pk := rv.FieldByIndex(m.byColumn[m.pk].index)
	genID := m.autoID && pk.IsZero()
	fields := insertFields(m, genID)

	if len(fields) == 0 {
		return fmt.Errorf("orm: Upsert %s: no columns to insert besides the generated key", m.table)
	}

	d := ex.Dialect()
	q := insertPrefix(ex, m, fields, 1) + conflictSQL(d, m, conflict, updates, genID && !capsOf(ex).Returning)

	args, err := rowArgs(ex, m, fields, rv)
	if err != nil {
		return err
	}

	if err := execUpsert(ctx, ex, m, q, args, pk, genID); err != nil {
		return err
	}

	if err := checkTrashed(ctx, ex, m, conflict, []reflect.Value{rv}); err != nil {
		if genID {
			_ = setID(pk, 0)
		}

		return err
	}

	return nil
}

// execUpsert runs the INSERT ... ON CONFLICT and writes a generated key into
// pk: from RETURNING, or from LAST_INSERT_ID(id) on MySQL.
func execUpsert(
	ctx context.Context, ex database.Executor, m *meta, q string, args []any, pk reflect.Value, genID bool,
) error {
	d := ex.Dialect()

	if genID && capsOf(ex).Returning {
		q += " RETURNING " + d.Quote(m.pk)

		// No row back: DoNothing on a live row, or a trashed row left alone.
		err := queryRowWrite(ctx, ex, rebind(q, d), args...).Scan(pk.Addr().Interface())
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("orm: upsert %s: %w", m.table, mapError(ex, err))
		}

		return nil
	}

	res, err := ex.ExecContext(ctx, rebind(q, d), args...)
	if err != nil {
		return fmt.Errorf("orm: upsert %s: %w", m.table, mapError(ex, err))
	}

	if !genID {
		return nil
	}

	// MySQL: LAST_INSERT_ID(id) in the update reports the id of an existing
	// row too (the ORM needs clientFoundRows=true).
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("orm: upsert %s: %w", m.table, err)
	}

	if err := setID(pk, id); err != nil {
		return fmt.Errorf("orm: upsert %s: %w", m.table, err)
	}

	return nil
}

// UpsertMany is Upsert for many models: multi-row INSERT statements under
// the dialect's parameter limit, in one transaction (the caller's when ex is
// a *database.Tx). It does not write generated keys back and runs no hooks.
// Like Upsert it never revives a trashed row: one among the models is an
// ErrConflict and rolls the whole batch back. The conflict columns must not
// be the generated key, and two models of one batch must not share a value of
// them (PostgreSQL rejects that: a row cannot be affected twice).
func UpsertMany[T any](ctx context.Context, ex database.Executor, models []T, on OnConflict) error {
	if len(models) == 0 {
		return nil
	}

	if err := checkDialect(ex); err != nil {
		return err
	}

	m := metaOf[T]()

	conflict, updates, err := upsertPlan(m, on)
	if err != nil {
		return err
	}

	now := clockFrom(ctx)

	for i := range models {
		rv := reflect.ValueOf(&models[i]).Elem()
		setTime(rv, m, "created_at", now, true)
		setTime(rv, m, "updated_at", now, true)
	}

	genID, err := generatedIDs(m, models)
	if err != nil {
		return err
	}

	if genID && slices.Contains(conflict, m.pk) {
		return fmt.Errorf(
			"orm: UpsertMany %s: the conflict columns include the generated key, which no row can share",
			m.table,
		)
	}

	fields := insertFields(m, genID)
	if len(fields) == 0 {
		return fmt.Errorf("orm: UpsertMany %s: no columns to insert besides the generated key", m.table)
	}

	perBatch := max(1, maxParams(ex.Dialect())/len(fields))

	return inTx(ctx, ex, func(ex database.Executor) error {
		d := ex.Dialect()
		tail := conflictSQL(d, m, conflict, updates, false)

		for batch := range slices.Chunk(models, perBatch) {
			args := make([]any, 0, len(batch)*len(fields))

			for i := range batch {
				row, err := rowArgs(ex, m, fields, reflect.ValueOf(&batch[i]).Elem())
				if err != nil {
					return err
				}

				args = append(args, row...)
			}

			q := insertPrefix(ex, m, fields, len(batch)) + tail
			if _, err := ex.ExecContext(ctx, rebind(q, d), args...); err != nil {
				return fmt.Errorf("orm: upsert %s: %w", m.table, mapError(ex, err))
			}
		}

		rows := make([]reflect.Value, len(models))
		for i := range models {
			rows[i] = reflect.ValueOf(&models[i]).Elem()
		}

		return checkTrashed(ctx, ex, m, conflict, rows)
	})
}

// insertPrefix is "INSERT INTO t (cols) VALUES (?, ...), ..." for rows rows.
func insertPrefix(ex database.Executor, m *meta, fields []field, rows int) string {
	d := ex.Dialect()

	cols := make([]string, len(fields))
	for i, f := range fields {
		cols[i] = d.Quote(f.column)
	}

	row := "(" + marks(len(fields)) + ")"

	return "INSERT INTO " + d.Quote(m.table) + " (" + strings.Join(cols, ", ") + ") VALUES " +
		strings.TrimSuffix(strings.Repeat(row+", ", rows), ", ")
}

// rowArgs binds the values of fields of the model in rv.
func rowArgs(ex database.Executor, m *meta, fields []field, rv reflect.Value) ([]any, error) {
	args := make([]any, 0, len(fields))

	for _, f := range fields {
		v, err := bindValue(ex, m.table, f.column, rv.FieldByIndex(f.index).Interface())
		if err != nil {
			return nil, err
		}

		args = append(args, v)
	}

	return args, nil
}
