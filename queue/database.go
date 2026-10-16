package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
)

// Database stores jobs in SQL tables (SQLite or Postgres). Workers reserve
// jobs atomically (Postgres uses FOR UPDATE SKIP LOCKED), so any number of
// worker processes can share the tables. A job whose worker died is
// reserved again once its timeout plus a grace period has passed: delivery
// is at least once, so handlers should be idempotent.
type Database struct {
	db     *database.DB
	jobs   string
	failed string
	grace  time.Duration
	now    func() time.Time
}

// DatabaseOptions name the tables.
type DatabaseOptions struct {
	// Table holds pending jobs (default "jobs").
	Table string
	// FailedTable holds failed jobs (default "failed_jobs").
	FailedTable string
	// Grace is added to a job's timeout before an abandoned reservation is
	// taken over (default 1 minute).
	Grace time.Duration
	// Now is the clock (default time.Now); tests set it.
	Now func() time.Time
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// NewDatabase returns a database driver; call CreateTables (or a
// migration with the same schema) before use.
func NewDatabase(db *database.DB, o DatabaseOptions) (*Database, error) {
	if o.Table == "" {
		o.Table = "jobs"
	}

	if o.FailedTable == "" {
		o.FailedTable = "failed_jobs"
	}

	if o.Grace <= 0 {
		o.Grace = time.Minute
	}

	for _, t := range []string{o.Table, o.FailedTable} {
		if !identRe.MatchString(t) {
			return nil, fmt.Errorf("queue: invalid table name %q", t)
		}
	}

	if o.Now == nil {
		o.Now = time.Now
	}

	return &Database{db: db, jobs: o.Table, failed: o.FailedTable, grace: o.Grace, now: o.Now}, nil
}

// q renders sqlText with table names ({j}, {f}) and dialect placeholders.
func (d *Database) q(dl database.Dialect, sqlText string) string {
	sqlText = strings.ReplaceAll(sqlText, "{j}", dl.Quote(d.jobs))
	sqlText = strings.ReplaceAll(sqlText, "{f}", dl.Quote(d.failed))

	var b strings.Builder

	n := 0

	for _, ch := range sqlText {
		if ch == '?' {
			n++
			b.WriteString(dl.Placeholder(n))

			continue
		}

		b.WriteRune(ch)
	}

	return b.String()
}

const jobColumns = `id, queue, kind, payload, attempts, max_retries, timeout_ms, available_at, created_at, last_error`

// CreateTables creates the jobs and failed jobs tables if missing.
func (d *Database) CreateTables(ctx context.Context) error {
	dl := d.db.Dialect()
	cols := `id TEXT PRIMARY KEY, queue TEXT NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0, max_retries INTEGER NOT NULL DEFAULT 0,
		timeout_ms BIGINT NOT NULL DEFAULT 0, available_at BIGINT NOT NULL, created_at BIGINT NOT NULL,
		last_error TEXT NOT NULL DEFAULT ''`

	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS {j} (` + cols + `, reserved_at BIGINT NOT NULL DEFAULT 0)`,
		`CREATE INDEX IF NOT EXISTS ` + dl.Quote(d.jobs+"_queue_available") + ` ON {j} (queue, available_at)`,
		`CREATE TABLE IF NOT EXISTS {f} (` + cols + `, failed_at BIGINT NOT NULL)`,
	} {
		if _, err := d.db.ExecContext(ctx, d.q(dl, stmt)); err != nil {
			return fmt.Errorf("queue: create tables: %w", err)
		}
	}

	return nil
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

// Push implements Driver.
func (d *Database) Push(ctx context.Context, m *Message) error { return d.PushTx(ctx, d.db, m) }

// PushTx inserts m with ex, so it is enqueued only if ex's transaction
// commits (Queue.DispatchTx).
func (d *Database) PushTx(ctx context.Context, ex database.Executor, m *Message) error {
	_, err := ex.ExecContext(
		ctx,
		d.q(ex.Dialect(), `INSERT INTO {j} (`+jobColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		m.ID,
		m.Queue,
		m.Kind,
		string(m.Payload),
		m.Attempts,
		m.MaxRetries,
		m.Timeout.Milliseconds(),
		ms(m.AvailableAt),
		ms(m.CreatedAt),
		m.LastError,
	)

	return err
}

// Pop implements Driver.
func (d *Database) Pop(ctx context.Context, queues []string) (*Message, error) {
	dl := d.db.Dialect()
	lock := ""

	if dl.Name() == "postgres" {
		lock = " FOR UPDATE SKIP LOCKED"
	}

	stmt := d.q(dl, `UPDATE {j} SET reserved_at = ? WHERE id = (
		SELECT id FROM {j} WHERE queue = ? AND available_at <= ?
			AND (reserved_at = 0 OR reserved_at + timeout_ms + ? <= ?)
		ORDER BY available_at, created_at, id LIMIT 1`+lock+`)
		RETURNING `+jobColumns)

	now := ms(d.now())

	for _, name := range queues {
		m, err := scanMessage(d.db.QueryRowContext(ctx, stmt, now, name, now, d.grace.Milliseconds(), now))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("queue: reserve: %w", err)
		}

		return m, nil
	}

	return nil, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanMessage(row scanner, extra ...any) (*Message, error) {
	var (
		m                    Message
		payload              string
		timeout, avail, made int64
	)

	dest := append([]any{
		&m.ID, &m.Queue, &m.Kind, &payload, &m.Attempts, &m.MaxRetries, &timeout, &avail, &made, &m.LastError,
	}, extra...)

	if err := row.Scan(dest...); err != nil {
		return nil, err
	}

	m.Payload = []byte(payload)
	m.Timeout = time.Duration(timeout) * time.Millisecond
	m.AvailableAt, m.CreatedAt = fromMS(avail), fromMS(made)

	return &m, nil
}

// Ack implements Driver.
func (d *Database) Ack(ctx context.Context, m *Message) error {
	_, err := d.db.ExecContext(ctx, d.q(d.db.Dialect(), `DELETE FROM {j} WHERE id = ?`), m.ID)
	return err
}

// Release implements Driver.
func (d *Database) Release(ctx context.Context, m *Message) error {
	_, err := d.db.ExecContext(ctx, d.q(d.db.Dialect(),
		`UPDATE {j} SET reserved_at = 0, attempts = ?, available_at = ?, last_error = ? WHERE id = ?`),
		m.Attempts, ms(m.AvailableAt), m.LastError, m.ID)

	return err
}

// Fail implements Driver: the job moves to the failed table.
func (d *Database) Fail(ctx context.Context, m *Message) error {
	return d.db.Tx(ctx, func(tx *database.Tx) error {
		_, err := tx.ExecContext(ctx, d.q(tx.Dialect(), `INSERT INTO {f} (`+jobColumns+`, failed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			m.ID, m.Queue, m.Kind, string(m.Payload), m.Attempts, m.MaxRetries, m.Timeout.Milliseconds(),
			ms(m.AvailableAt), ms(m.CreatedAt), m.LastError, ms(d.now()))
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, d.q(tx.Dialect(), `DELETE FROM {j} WHERE id = ?`), m.ID)

		return err
	})
}

// Failed implements FailedStore, newest first.
func (d *Database) Failed(ctx context.Context) ([]FailedJob, error) {
	rows, err := d.db.QueryContext(ctx, d.q(d.db.Dialect(),
		`SELECT `+jobColumns+`, failed_at FROM {f} ORDER BY failed_at DESC, id`))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FailedJob

	for rows.Next() {
		var at int64

		m, err := scanMessage(rows, &at)
		if err != nil {
			return nil, err
		}

		out = append(out, FailedJob{Message: *m, FailedAt: fromMS(at)})
	}

	return out, rows.Err()
}

// Retry implements FailedStore: the jobs (all when ids is empty) go back
// to the queue with their attempts reset.
func (d *Database) Retry(ctx context.Context, ids ...string) (int, error) {
	n := 0

	err := d.db.Tx(ctx, func(tx *database.Tx) error {
		where, args := idFilter(ids)
		now := ms(d.now())

		res, err := tx.ExecContext(ctx, d.q(tx.Dialect(), `INSERT INTO {j} (`+jobColumns+`)
			SELECT id, queue, kind, payload, 0, max_retries, timeout_ms, ?, created_at, last_error FROM {f}`+where),
			append([]any{now}, args...)...)
		if err != nil {
			return err
		}

		affected, err := res.RowsAffected()
		n = int(affected)

		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, d.q(tx.Dialect(), `DELETE FROM {f}`+where), args...)

		return err
	})

	return n, err
}

// Forget implements FailedStore: it deletes failed jobs (all when ids is
// empty).
func (d *Database) Forget(ctx context.Context, ids ...string) (int, error) {
	where, args := idFilter(ids)

	res, err := d.db.ExecContext(ctx, d.q(d.db.Dialect(), `DELETE FROM {f}`+where), args...)
	if err != nil {
		return 0, err
	}

	n, err := res.RowsAffected()

	return int(n), err
}

func idFilter(ids []string) (string, []any) {
	if len(ids) == 0 {
		return "", nil
	}

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	return " WHERE id IN (" + strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ") + ")", args
}

// Pending returns the number of jobs waiting or running.
func (d *Database) Pending(ctx context.Context) (int, error) {
	var n int

	err := d.db.QueryRowContext(ctx, d.q(d.db.Dialect(), `SELECT COUNT(*) FROM {j}`)).Scan(&n)

	return n, err
}
