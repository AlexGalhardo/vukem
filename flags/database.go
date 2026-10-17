package flags

import (
	"context"
	"fmt"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

// Table is the table holding stored flag values.
const Table = "feature_flags"

// Migration creates the flag table; register it with a timestamped id
// (vukem install:flags does):
//
//	flags.Migration("20261017120000_create_feature_flags_table")
func Migration(id string) schema.Migration {
	return schema.Migration{
		ID: id,
		Up: func(s *schema.Schema) error {
			return s.Create(Table, func(t *schema.Table) {
				t.ID()
				t.String("name", 100)
				t.String("scope", maxScope).Default("")
				t.Text("value")
				t.Timestamp("updated_at")
				t.UniqueIndex("name", "scope")
			})
		},
		Down: func(s *schema.Schema) error { return s.DropIfExists(Table) },
	}
}

// DatabaseStore keeps stored values in Table (SQLite, Postgres or MySQL).
type DatabaseStore struct {
	db  *database.DB
	now func() time.Time
}

// NewDatabaseStore returns a store over db; now stamps updated_at
// (time.Now when nil).
func NewDatabaseStore(db *database.DB, now func() time.Time) *DatabaseStore {
	if now == nil {
		now = time.Now
	}

	return &DatabaseStore{db: db, now: now}
}

func (s *DatabaseStore) q(sqlText string) string { return database.Rebind(s.db.Dialect(), sqlText) }

// Load implements Store.
func (s *DatabaseStore) Load(ctx context.Context, scope Scope) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, s.q("SELECT name, value FROM "+Table+" WHERE scope = ?"), string(scope))
	if err != nil {
		return nil, fmt.Errorf("load flags: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}

	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("scan flag: %w", err)
		}

		out[name] = value
	}

	return out, rows.Err()
}

// Save implements Store.
func (s *DatabaseStore) Save(ctx context.Context, scope Scope, name, value string) error {
	stmt := `INSERT INTO ` + Table + ` (name, scope, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (name, scope) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`
	if s.db.Dialect().Name() == "mysql" {
		stmt = `INSERT INTO ` + Table + ` (name, scope, value, updated_at) VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at)`
	}

	_, err := s.db.ExecContext(ctx, s.q(stmt), name, string(scope), value, s.now().UTC())

	return err
}

// Delete implements Store.
func (s *DatabaseStore) Delete(ctx context.Context, scope Scope, name string) error {
	_, err := s.db.ExecContext(ctx, s.q("DELETE FROM "+Table+" WHERE name = ? AND scope = ?"), name, string(scope))
	return err
}
