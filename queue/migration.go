package queue

import (
	"fmt"

	"github.com/AlexGalhardo/vukem/schema"
)

// MigrationID is the id of the migration Migrations returns; it names the
// framework's own migration in vukem migrate:status.
const MigrationID = "20261018000000_vukem_create_queue_tables"

// Migrations returns the migration that creates the queue tables named by
// o, so an app can manage them with vukem migrate instead of leaving them
// to the boot-time CreateTables:
//
//	vukem.WithMigrations(slices.Concat(
//		stores.Migrations(stores.Options{}),
//		migrations.All(),
//	))
//
// The migration runs the same statements as CreateTables, which stays as
// the fallback for apps that do not register it. On a database where the
// tables already exist (the driver created them before the app opted in)
// the migration records itself and changes nothing, so an upgrade never
// fails on a conflicting CREATE TABLE; Down drops both tables.
func Migrations(o DatabaseOptions) []schema.Migration {
	jobs, failed := tableNames(o)

	return []schema.Migration{{
		ID: MigrationID,
		Up: func(s *schema.Schema) error {
			if err := checkTables(jobs, failed); err != nil {
				return err
			}

			has, err := s.HasTable(jobs)
			if err != nil {
				return fmt.Errorf("queue: look for table %s: %w", jobs, err)
			}

			if has {
				return nil
			}

			for _, stmt := range tableDDL(s.SQLDialect(), jobs, failed) {
				if err := s.Raw(stmt); err != nil {
					return err
				}
			}

			return nil
		},
		Down: func(s *schema.Schema) error {
			if err := checkTables(jobs, failed); err != nil {
				return err
			}

			if err := s.DropIfExists(failed); err != nil {
				return err
			}

			return s.DropIfExists(jobs)
		},
	}}
}
