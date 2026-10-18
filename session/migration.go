package session

import (
	"fmt"

	"github.com/AlexGalhardo/vukem/schema"
)

// MigrationID is the id of the migration Migrations returns; it names the
// framework's own migration in vukem migrate:status.
const MigrationID = "20261018000001_vukem_create_sessions_table"

// Migrations returns the migration that creates the sessions table (the
// default when table is empty), so an app can manage it with vukem
// migrate instead of leaving it to the boot-time CreateTable. The
// statements are the ones CreateTable runs, which stays as the fallback
// for apps that do not register it.
//
// The DDL follows the dialect the migration runs on, so the same
// migration serves SQLite, PostgreSQL and MySQL. On a database where the
// table already exists (the store created it before the app opted in) the
// migration records itself and changes nothing; Down drops the table.
func Migrations(table string) []schema.Migration {
	if table == "" {
		table = DefaultTable
	}

	return []schema.Migration{{
		ID: MigrationID,
		Up: func(s *schema.Schema) error {
			if err := checkTable(table); err != nil {
				return err
			}

			has, err := s.HasTable(table)
			if err != nil {
				return fmt.Errorf("session: look for table %s: %w", table, err)
			}

			if has {
				return nil
			}

			for _, stmt := range tableDDL(s.Dialect() == "mysql", table) {
				if err := s.Raw(stmt); err != nil {
					return err
				}
			}

			return nil
		},
		Down: func(s *schema.Schema) error {
			if err := checkTable(table); err != nil {
				return err
			}

			return s.DropIfExists(table)
		},
	}}
}
