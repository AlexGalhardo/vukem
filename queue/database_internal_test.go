package queue

import (
	"errors"
	"fmt"
	"testing"
)

type sqlStateError string

func (e sqlStateError) Error() string    { return "pg error" }
func (e sqlStateError) SQLState() string { return string(e) }

func TestUnknownColumn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"sqlite", errors.New("SQL logic error: no such column: seq (1)"), true},
		{"postgres sqlstate", fmt.Errorf("query: %w", sqlStateError("42703")), true},
		{"postgres other sqlstate", sqlStateError("42501"), false},
		{"mysql", errors.New("Error 1054 (42S22): Unknown column 'seq' in 'field list'"), true},
		{"permission", errors.New("Error 1142 (42000): SELECT command denied to user"), false},
		{"connection", errors.New("dial tcp 127.0.0.1:5432: connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := unknownColumn(tt.err); got != tt.want {
				t.Fatalf("unknownColumn(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
