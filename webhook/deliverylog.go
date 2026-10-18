package webhook

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
)

// Attempt is one outgoing delivery attempt. It holds no URL, secret or
// payload.
type Attempt struct {
	Endpoint string
	EventID  string
	// Status is the HTTP status, 0 when no response arrived.
	Status   int
	Duration time.Duration
	// Error is empty on success or one of the Attempt* categories.
	Error string
	At    time.Time
}

// DeliveryLog stores delivery attempts, for example to show a seller why
// their endpoint is failing. It is the extension point for apps that
// already have a deliveries table: implement Record (or wrap a function in
// DeliveryLogFunc) and map Attempt onto your own columns; SQLDeliveryLog is
// one implementation with a fixed schema.
//
// The Sender calls Record after every attempt, from concurrent queue
// workers, with a context that outlives the job (5s timeout). A Record
// error is logged and never retries or fails the delivery.
type DeliveryLog interface {
	Record(ctx context.Context, a Attempt) error
}

// DeliveryLogFunc adapts a function to DeliveryLog.
type DeliveryLogFunc func(ctx context.Context, a Attempt) error

// Record implements DeliveryLog.
func (f DeliveryLogFunc) Record(ctx context.Context, a Attempt) error { return f(ctx, a) }

// SQLDeliveryLog keeps attempts in a table (SQLite, Postgres or MySQL):
// id, endpoint, event_id, status, duration_ms, error and attempted_at in
// Unix milliseconds. CreateTable creates it. Prune old rows with a
// scheduled DELETE if the table grows too large. Only the table name is
// configurable; to write into an existing table with other columns,
// implement DeliveryLog instead.
type SQLDeliveryLog struct {
	db    *database.DB
	table string
}

// The table name is at most 51 bytes so the index name ("_endpoint_at"
// suffix) stays within the 63-byte identifier limit of Postgres and MySQL.
var logTableRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,50}$`)

const (
	maxErrorLen = 1000
	maxRecent   = 1000
)

// DefaultDeliveryTable is the table SQLDeliveryLog owns when
// NewSQLDeliveryLog is called with an empty name. It is listed with every
// other table vukem owns in docs/database.md#tables-the-framework-owns.
const DefaultDeliveryTable = "webhook_deliveries"

// checkLogTable rejects a name that is not a plain identifier of at most
// 51 bytes: it is interpolated into SQL, values never are.
func checkLogTable(table string) error {
	if !logTableRe.MatchString(table) {
		return fmt.Errorf("webhook: invalid table name %q", table)
	}

	return nil
}

// NewSQLDeliveryLog returns a log on table (default "webhook_deliveries").
func NewSQLDeliveryLog(db *database.DB, table string) (*SQLDeliveryLog, error) {
	if table == "" {
		table = DefaultDeliveryTable
	}

	if err := checkLogTable(table); err != nil {
		return nil, err
	}

	return &SQLDeliveryLog{db: db, table: table}, nil
}

// q renders sqlText with the table name ({t}) and dialect placeholders.
func (l *SQLDeliveryLog) q(sqlText string) string { return render(l.db.Dialect(), l.table, sqlText) }

// render substitutes the table name ({t}) and dialect placeholders.
func render(dl database.Dialect, table, sqlText string) string {
	var b strings.Builder

	n := 0

	for _, ch := range strings.ReplaceAll(sqlText, "{t}", dl.Quote(table)) {
		if ch == '?' {
			n++
			b.WriteString(dl.Placeholder(n))

			continue
		}

		b.WriteRune(ch)
	}

	return b.String()
}

// tableDDL returns the statements that create table and its
// (endpoint, attempted_at) index on dl. CreateTable runs them at boot and
// the migration from Migrations runs the same list, so the two paths
// cannot drift (TestMigrationMatchesCreateTableDDL).
func tableDDL(dl database.Dialect, table string) []string {
	cols := `endpoint TEXT NOT NULL, event_id TEXT NOT NULL, status INTEGER NOT NULL,
		duration_ms BIGINT NOT NULL, error TEXT NOT NULL, attempted_at BIGINT NOT NULL`
	index := dl.Quote(table + "_endpoint_at")

	var stmts []string

	switch dl.Name() {
	case "mysql":
		// No CREATE INDEX IF NOT EXISTS on MySQL, and TEXT cannot be indexed.
		stmts = []string{`CREATE TABLE IF NOT EXISTS {t} (id BIGINT AUTO_INCREMENT PRIMARY KEY,
			endpoint VARCHAR(255) NOT NULL, event_id VARCHAR(255) NOT NULL, status INT NOT NULL,
			duration_ms BIGINT NOT NULL, error TEXT NOT NULL, attempted_at BIGINT NOT NULL,
			INDEX ` + index + ` (endpoint, attempted_at))`}
	case "postgres":
		stmts = []string{`CREATE TABLE IF NOT EXISTS {t} (id BIGSERIAL PRIMARY KEY, ` + cols + `)`}
	default:
		stmts = []string{`CREATE TABLE IF NOT EXISTS {t} (id INTEGER PRIMARY KEY AUTOINCREMENT, ` + cols + `)`}
	}

	if dl.Name() != "mysql" {
		stmts = append(stmts, `CREATE INDEX IF NOT EXISTS `+index+` ON {t} (endpoint, attempted_at)`)
	}

	for i, stmt := range stmts {
		stmts[i] = render(dl, table, stmt)
	}

	return stmts
}

// CreateTable creates the table and its (endpoint, attempted_at) index if
// missing. It is the fallback for apps that do not register Migrations:
// the statements are the same, so running it after the migration changes
// nothing.
func (l *SQLDeliveryLog) CreateTable(ctx context.Context) error {
	for _, stmt := range tableDDL(l.db.Dialect(), l.table) {
		if _, err := l.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("webhook: create delivery log: %w", err)
		}
	}

	return nil
}

// Record implements DeliveryLog. Errors longer than 1000 bytes are cut.
func (l *SQLDeliveryLog) Record(ctx context.Context, a Attempt) error {
	// MySQL indexes these as VARCHAR(255); refuse rather than truncate, as a
	// cut id would point at another endpoint or event.
	if len(a.Endpoint) > maxIDLen || len(a.EventID) > maxIDLen {
		return errors.New("webhook: record attempt: endpoint and event id must be at most 255 bytes")
	}

	msg := a.Error
	if len(msg) > maxErrorLen {
		msg = strings.ToValidUTF8(msg[:maxErrorLen], "")
	}

	_, err := l.db.ExecContext(ctx, l.q(`INSERT INTO {t} (endpoint, event_id, status, duration_ms, error, attempted_at)
		VALUES (?, ?, ?, ?, ?, ?)`), a.Endpoint, a.EventID, a.Status, a.Duration.Milliseconds(), msg, a.At.UnixMilli())
	if err != nil {
		return fmt.Errorf("webhook: record attempt: %w", err)
	}

	return nil
}

// Recent returns the latest limit attempts (1 to 1000) for endpoint,
// newest first.
func (l *SQLDeliveryLog) Recent(ctx context.Context, endpoint string, limit int) ([]Attempt, error) {
	rows, err := l.db.QueryContext(ctx, l.q(`SELECT endpoint, event_id, status, duration_ms, error, attempted_at
		FROM {t} WHERE endpoint = ? ORDER BY attempted_at DESC, id DESC LIMIT ?`), endpoint, min(max(limit, 1), maxRecent))
	if err != nil {
		return nil, fmt.Errorf("webhook: recent attempts: %w", err)
	}
	defer rows.Close()

	var out []Attempt

	for rows.Next() {
		var (
			a      Attempt
			ms, at int64
		)

		if err := rows.Scan(&a.Endpoint, &a.EventID, &a.Status, &ms, &a.Error, &at); err != nil {
			return nil, fmt.Errorf("webhook: recent attempts: %w", err)
		}

		a.Duration = time.Duration(ms) * time.Millisecond
		a.At = time.UnixMilli(at).UTC()
		out = append(out, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("webhook: recent attempts: %w", err)
	}

	return out, nil
}
