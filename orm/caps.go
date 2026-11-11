package orm

import (
	"context"
	"database/sql"
	"errors"

	"github.com/AlexGalhardo/vukem/database"
)

// capsOf is the capabilities of ex's dialect.
func capsOf(ex database.Executor) database.Capabilities {
	return database.MustCapabilities(ex.Dialect())
}

// errUnknownDialect is what an executor whose dialect does not describe
// itself (database.Capable) gets from every ORM call: fail closed instead of
// guessing a grammar.
var errUnknownDialect = errors.New("orm: unknown dialect family (the dialect must implement database.Capable)")

// checkDialect fails when ex's dialect has no capabilities.
func checkDialect(ex database.Executor) error {
	if capsOf(ex).Family == "" {
		return errUnknownDialect
	}

	return nil
}

// queryRowWrite runs a single-row statement that writes (INSERT ...
// RETURNING) on the writer pool when ex has one; *database.DB routes by
// pool, so the statement must not depend on its text.
func queryRowWrite(ctx context.Context, ex database.Executor, q string, args ...any) *sql.Row {
	if w, ok := ex.(interface {
		QueryRowWriteContext(context.Context, string, ...any) *sql.Row
	}); ok {
		return w.QueryRowWriteContext(ctx, q, args...)
	}

	return ex.QueryRowContext(ctx, q, args...)
}

// queryWrite is queryRowWrite for a statement that returns several rows.
func queryWrite(ctx context.Context, ex database.Executor, q string, args ...any) (*sql.Rows, error) {
	if w, ok := ex.(interface {
		QueryWriteContext(context.Context, string, ...any) (*sql.Rows, error)
	}); ok {
		return w.QueryWriteContext(ctx, q, args...)
	}

	return ex.QueryContext(ctx, q, args...)
}
