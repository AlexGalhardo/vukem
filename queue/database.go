package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AlexGalhardo/vukem/database"
)

// Database stores jobs in SQL tables (SQLite, Postgres or MySQL). Workers
// reserve jobs atomically (Postgres and MySQL use FOR UPDATE SKIP LOCKED),
// so any number of
// worker processes can share the tables. A job whose worker died is
// reserved again once its timeout plus a grace period has passed: delivery
// is at least once, so handlers should be idempotent.
type Database struct {
	db     *database.DB
	jobs   string
	failed string
	grace  time.Duration
	now    atomic.Pointer[func() time.Time]
	// reserved maps each *Message handed out by Pop to its reserved_at,
	// so a worker that ran past its timeout cannot settle the job after
	// another worker re-reserved it.
	reserved sync.Map
	// seq is the last value handed out by nextSeq.
	seq atomic.Int64
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
	// Now is the clock that decides when delayed and abandoned jobs are
	// due (default time.Now); app.Queue passes the app clock.
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

	d := &Database{db: db, jobs: o.Table, failed: o.FailedTable, grace: o.Grace}
	d.now.Store(&o.Now)

	return d, nil
}

// SetClock replaces DatabaseOptions.Now; it implements Clock.
func (d *Database) SetClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}

	d.now.Store(&now)
}

func (d *Database) clock() time.Time { return (*d.now.Load())() }

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

// popOrder is FIFO: seq breaks ties between jobs available and created in
// the same millisecond; id only matters for rows of the same seq (rows
// that predate the column, and a Retry batch, which shares one seq).
const popOrder = ` ORDER BY available_at, created_at, seq, id`

// seqColumn is the tiebreaker the jobs table gained in v0.26.0;
// CreateTables adds it to older tables.
const seqColumn = `seq BIGINT NOT NULL DEFAULT 0`

// nextSeq returns a value greater than any it returned before, seeded by
// the wall clock: jobs pushed by one process in the same millisecond pop
// in push order, and jobs from several processes interleave roughly by
// real time.
func (d *Database) nextSeq() int64 {
	for {
		last := d.seq.Load()

		next := max(last+1, time.Now().UnixNano())
		if d.seq.CompareAndSwap(last, next) {
			return next
		}
	}
}

// CreateTables creates the jobs and failed jobs tables if missing.
func (d *Database) CreateTables(ctx context.Context) error {
	dl := d.db.Dialect()
	if dl.Name() == "mysql" {
		return d.createMySQLTables(ctx, dl)
	}

	cols := `id TEXT PRIMARY KEY, queue TEXT NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0, max_retries INTEGER NOT NULL DEFAULT 0,
		timeout_ms BIGINT NOT NULL DEFAULT 0, available_at BIGINT NOT NULL, created_at BIGINT NOT NULL,
		last_error TEXT NOT NULL DEFAULT ''`

	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS {j} (` + cols + `, reserved_at BIGINT NOT NULL DEFAULT 0, ` + seqColumn + `)`,
		`CREATE INDEX IF NOT EXISTS ` + dl.Quote(d.jobs+"_queue_available") + ` ON {j} (queue, available_at)`,
		`CREATE TABLE IF NOT EXISTS {f} (` + cols + `, failed_at BIGINT NOT NULL)`,
	} {
		if _, err := d.db.ExecContext(ctx, d.q(dl, stmt)); err != nil {
			return fmt.Errorf("queue: create tables: %w", err)
		}
	}

	return d.ensureSeq(ctx, dl)
}

// ensureSeq adds the seq column to a jobs table created before it existed.
// MySQL and SQLite have no ADD COLUMN IF NOT EXISTS, so it probes first
// and alters only on an unknown-column error; any other probe error
// (permissions, connection) is returned. When two processes boot at once
// and the ALTER loses the race, the second probe sees the column and the
// ALTER error is dropped.
func (d *Database) ensureSeq(ctx context.Context, dl database.Dialect) error {
	stmt := d.q(dl, `SELECT seq FROM {j} WHERE 1 = 0`)

	probe := func() (bool, error) {
		var v int64

		switch err := d.db.QueryRowContext(ctx, stmt).Scan(&v); {
		case err == nil, errors.Is(err, sql.ErrNoRows):
			return true, nil
		case unknownColumn(err):
			return false, nil
		default:
			return false, err
		}
	}

	exists, err := probe()
	if err != nil {
		return fmt.Errorf("queue: probe seq column: %w", err)
	}

	if exists {
		return nil
	}

	if _, alterErr := d.db.ExecContext(ctx, d.q(dl, `ALTER TABLE {j} ADD COLUMN `+seqColumn)); alterErr != nil {
		if exists, err := probe(); err != nil || !exists {
			return fmt.Errorf("queue: add seq column: %w", errors.Join(alterErr, err))
		}
	}

	return nil
}

// unknownColumn reports whether err is the dialect's "column does not
// exist" error: SQLSTATE 42703 on PostgreSQL (pgx errors expose
// SQLState), error 1054 on MySQL and MariaDB, "no such column" on SQLite.
// Core imports no driver, so MySQL and SQLite are matched on the message.
func unknownColumn(err error) bool {
	if e, ok := errors.AsType[interface {
		error
		SQLState() string
	}](err); ok {
		return e.SQLState() == "42703"
	}

	msg := err.Error()

	return strings.Contains(msg, "no such column") || strings.Contains(msg, "Error 1054")
}

// createMySQLTables uses binary keys (exact, case-sensitive ids), keeps the
// index inline (MySQL has no CREATE INDEX IF NOT EXISTS) and drops the
// TEXT default MySQL 8 refuses; inserts always set last_error.
func (d *Database) createMySQLTables(ctx context.Context, dl database.Dialect) error {
	cols := `id VARBINARY(255) NOT NULL PRIMARY KEY, queue VARBINARY(255) NOT NULL, kind VARCHAR(255) NOT NULL,
		payload LONGTEXT NOT NULL, attempts INT NOT NULL DEFAULT 0, max_retries INT NOT NULL DEFAULT 0,
		timeout_ms BIGINT NOT NULL DEFAULT 0, available_at BIGINT NOT NULL, created_at BIGINT NOT NULL,
		last_error LONGTEXT NOT NULL`

	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS {j} (` + cols + `, reserved_at BIGINT NOT NULL DEFAULT 0, ` + seqColumn + `, INDEX ` +
			dl.Quote(d.jobs+"_queue_available") + ` (queue, available_at))`,
		`CREATE TABLE IF NOT EXISTS {f} (` + cols + `, failed_at BIGINT NOT NULL)`,
	} {
		if _, err := d.db.ExecContext(ctx, d.q(dl, stmt)); err != nil {
			return fmt.Errorf("queue: create tables: %w", err)
		}
	}

	return d.ensureSeq(ctx, dl)
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
		d.q(ex.Dialect(), `INSERT INTO {j} (`+jobColumns+`, seq) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
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
		d.nextSeq(),
	)

	return err
}

// Pop implements Driver.
func (d *Database) Pop(ctx context.Context, queues []string) (*Message, error) {
	dl := d.db.Dialect()
	if dl.Name() == "mysql" {
		return d.popMySQL(ctx, dl, queues)
	}

	lock := ""

	if dl.Name() == "postgres" {
		lock = " FOR UPDATE SKIP LOCKED"
	}

	stmt := d.q(dl, `UPDATE {j} SET reserved_at = ? WHERE id = (
		SELECT id FROM {j} WHERE queue = ? AND available_at <= ?
			AND (reserved_at = 0 OR reserved_at + timeout_ms + ? <= ?)
		`+popOrder+` LIMIT 1`+lock+`)
		RETURNING `+jobColumns)

	now := ms(d.clock())

	for _, name := range queues {
		m, err := scanMessage(d.db.QueryRowContext(ctx, stmt, now, name, now, d.grace.Milliseconds(), now))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("queue: reserve: %w", err)
		}

		d.reserved.Store(m, now)

		return m, nil
	}

	return nil, nil
}

// popMySQL reserves a job in a transaction: MySQL has no RETURNING and
// cannot UPDATE a table it selects from in a subquery, so the row is
// locked with SKIP LOCKED (MySQL 8, MariaDB 10.6+) and then updated.
func (d *Database) popMySQL(ctx context.Context, dl database.Dialect, queues []string) (*Message, error) {
	sel := d.q(dl, `SELECT `+jobColumns+` FROM {j} WHERE queue = ? AND available_at <= ?
			AND (reserved_at = 0 OR reserved_at + timeout_ms + ? <= ?)
		`+popOrder+` LIMIT 1 FOR UPDATE SKIP LOCKED`)
	upd := d.q(dl, `UPDATE {j} SET reserved_at = ? WHERE id = ?`)

	now := ms(d.clock())

	for _, name := range queues {
		var m *Message

		err := d.db.Tx(ctx, func(tx *database.Tx) error {
			var err error

			m, err = scanMessage(tx.QueryRowContext(ctx, sel, name, now, d.grace.Milliseconds(), now))
			if err != nil {
				return err
			}

			_, err = tx.ExecContext(ctx, upd, now, m.ID)

			return err
		})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("queue: reserve: %w", err)
		}

		d.reserved.Store(m, now)

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

// ErrReservationLost is returned by Ack, Release and Fail when the job's
// reservation expired and another worker reserved it again: the job
// belongs to that worker now, so the stale result is dropped.
var ErrReservationLost = errors.New("queue: job reservation lost to another worker")

// owns returns the WHERE clause and arguments matching m only while it
// still holds the reservation Pop gave it. Messages that did not come
// from this driver's Pop match by id.
func (d *Database) owns(m *Message) (string, []any) {
	if at, ok := d.reserved.LoadAndDelete(m); ok {
		return ` WHERE id = ? AND reserved_at = ?`, []any{m.ID, at}
	}

	return ` WHERE id = ?`, []any{m.ID}
}

func settled(res sql.Result, err error) error {
	if err != nil {
		return err
	}

	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrReservationLost
	}

	return nil
}

// Ack implements Driver.
func (d *Database) Ack(ctx context.Context, m *Message) error {
	where, args := d.owns(m)
	return settled(d.db.ExecContext(ctx, d.q(d.db.Dialect(), `DELETE FROM {j}`+where), args...))
}

// Release implements Driver.
func (d *Database) Release(ctx context.Context, m *Message) error {
	where, args := d.owns(m)

	return settled(d.db.ExecContext(ctx, d.q(d.db.Dialect(),
		`UPDATE {j} SET reserved_at = 0, attempts = ?, available_at = ?, last_error = ?`+where),
		append([]any{m.Attempts, ms(m.AvailableAt), m.LastError}, args...)...))
}

// Fail implements Driver: the job moves to the failed table.
func (d *Database) Fail(ctx context.Context, m *Message) error {
	where, args := d.owns(m)

	return d.db.Tx(ctx, func(tx *database.Tx) error {
		// Delete first: if the reservation was lost, nothing is copied.
		if err := settled(tx.ExecContext(ctx, d.q(tx.Dialect(), `DELETE FROM {j}`+where), args...)); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx, d.q(tx.Dialect(), `INSERT INTO {f} (`+jobColumns+`, failed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			m.ID, m.Queue, m.Kind, string(m.Payload), m.Attempts, m.MaxRetries, m.Timeout.Milliseconds(),
			ms(m.AvailableAt), ms(m.CreatedAt), m.LastError, ms(d.clock()))

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
// to the queue with their attempts reset. The batch shares one seq, so
// retried jobs of the same millisecond pop in id order.
func (d *Database) Retry(ctx context.Context, ids ...string) (int, error) {
	n := 0

	err := d.db.Tx(ctx, func(tx *database.Tx) error {
		where, args := idFilter(ids)
		now := ms(d.clock())

		res, err := tx.ExecContext(ctx, d.q(tx.Dialect(), `INSERT INTO {j} (`+jobColumns+`, seq)
			SELECT id, queue, kind, payload, 0, max_retries, timeout_ms, ?, created_at, last_error, ? FROM {f}`+where),
			append([]any{now, d.nextSeq()}, args...)...)
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
