package webhook

import (
	"context"
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
	// Error is the transport error (refused, timeout, blocked address).
	Error string
	At    time.Time
}

// DeliveryLog stores delivery attempts, for example to show a seller why
// their endpoint is failing.
type DeliveryLog interface {
	Record(ctx context.Context, a Attempt) error
}

// SQLDeliveryLog keeps attempts in a table (SQLite, Postgres or MySQL):
// id, endpoint, event_id, status, duration_ms, error and attempted_at in
// Unix milliseconds. CreateTable creates it. Prune old rows with a
// scheduled DELETE if the table grows too large.
type SQLDeliveryLog struct {
	db    *database.DB
	table string
}

var logTableRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const maxErrorLen = 1000

// NewSQLDeliveryLog returns a log on table (default "webhook_deliveries").
func NewSQLDeliveryLog(db *database.DB, table string) (*SQLDeliveryLog, error) {
	if table == "" {
		table = "webhook_deliveries"
	}

	if !logTableRe.MatchString(table) {
		return nil, fmt.Errorf("webhook: invalid table name %q", table)
	}

	return &SQLDeliveryLog{db: db, table: table}, nil
}

// q renders sqlText with the table name ({t}) and dialect placeholders.
func (l *SQLDeliveryLog) q(sqlText string) string {
	dl := l.db.Dialect()

	var b strings.Builder

	n := 0

	for _, ch := range strings.ReplaceAll(sqlText, "{t}", dl.Quote(l.table)) {
		if ch == '?' {
			n++
			b.WriteString(dl.Placeholder(n))

			continue
		}

		b.WriteRune(ch)
	}

	return b.String()
}

// CreateTable creates the table and its (endpoint, attempted_at) index if
// missing.
func (l *SQLDeliveryLog) CreateTable(ctx context.Context) error {
	dl := l.db.Dialect()
	cols := `endpoint TEXT NOT NULL, event_id TEXT NOT NULL, status INTEGER NOT NULL,
		duration_ms BIGINT NOT NULL, error TEXT NOT NULL, attempted_at BIGINT NOT NULL`
	index := dl.Quote(l.table + "_endpoint_at")

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

	for _, stmt := range stmts {
		if _, err := l.db.ExecContext(ctx, l.q(stmt)); err != nil {
			return fmt.Errorf("webhook: create delivery log: %w", err)
		}
	}

	return nil
}

// Record implements DeliveryLog. Errors longer than 1000 bytes are cut.
func (l *SQLDeliveryLog) Record(ctx context.Context, a Attempt) error {
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

// Recent returns the latest limit attempts for endpoint, newest first.
func (l *SQLDeliveryLog) Recent(ctx context.Context, endpoint string, limit int) ([]Attempt, error) {
	rows, err := l.db.QueryContext(ctx, l.q(`SELECT endpoint, event_id, status, duration_ms, error, attempted_at
		FROM {t} WHERE endpoint = ? ORDER BY attempted_at DESC, id DESC LIMIT ?`), endpoint, max(limit, 1))
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
