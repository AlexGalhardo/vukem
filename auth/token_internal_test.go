package auth

import (
	"errors"
	"fmt"
	"testing"
)

// sqlStateError mimics *pgconn.PgError.
type sqlStateError struct{ code, msg string }

func (e sqlStateError) Error() string    { return e.msg }
func (e sqlStateError) SQLState() string { return e.code }

func TestMissingRevokedAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			"postgres undefined column",
			fmt.Errorf(
				"query: %w",
				sqlStateError{"42703", `ERROR: column "revoked_at" does not exist (SQLSTATE 42703)`},
			),
			true,
		},
		{
			"postgres other code naming the column",
			sqlStateError{"57P01", `terminating connection while reading revoked_at`},
			false,
		},
		{"sqlite", errors.New("SQL logic error: no such column: revoked_at (1)"), true},
		{"mysql", errors.New("Error 1054 (42S22): Unknown column 'revoked_at' in 'field list'"), true},
		{"another missing column", errors.New("SQL logic error: no such column: hash (1)"), false},
		{"connection refused", errors.New("dial tcp: connection refused"), false},
		{"text naming the column only", errors.New("timeout while scanning revoked_at"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := missingRevokedAt(tt.err); got != tt.want {
				t.Errorf("missingRevokedAt(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
