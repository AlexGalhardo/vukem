package studio

import (
	"context"
	"database/sql"
	"errors"
)

// Schema implements SQLSource.
func (s *SQLDB) Schema(ctx context.Context) ([]Table, error) {
	ts, err := s.catalog.Tables(ctx)
	if err != nil {
		return nil, &queryError{err}
	}

	return ts, nil
}

// CanShowCreate implements SQLSource: the database reports a CREATE statement
// through a read-only query, SQLite from sqlite_master and MySQL with SHOW
// CREATE TABLE. PostgreSQL has no such statement, so Studio shows none rather
// than rebuild one.
func (s *SQLDB) CanShowCreate() bool {
	f := s.family()

	return f == "sqlite" || f == "mysql"
}

// CreateStatement implements SQLSource. The name is checked against the
// catalog first, so the identifier that reaches SHOW CREATE TABLE is one the
// database itself listed.
func (s *SQLDB) CreateStatement(ctx context.Context, table string) (string, bool, error) {
	if !s.CanShowCreate() {
		return "", false, nil
	}

	t, err := s.Table(ctx, table)
	if err != nil {
		return "", false, err
	}

	var text string

	switch s.family() {
	case "mysql":
		var name string

		err = s.db.QueryRowContext(ctx, "SHOW CREATE TABLE "+s.db.Dialect().Quote(t.Name)).Scan(&name, &text)
	default:
		var stored sql.NullString

		err = s.db.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", t.Name).
			Scan(&stored)
		text = stored.String
	}

	if errors.Is(err, sql.ErrNoRows) || (err == nil && text == "") {
		return "", false, nil
	}

	if err != nil {
		return "", false, &queryError{err}
	}

	return text, true, nil
}
