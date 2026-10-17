package cache

import (
	"errors"
	"testing"
)

func TestRetryOnceOnConflict(t *testing.T) {
	t.Parallel()

	deadlock := errors.New("ERROR: deadlock detected (SQLSTATE 40P01)")
	other := errors.New("ERROR: relation does not exist (SQLSTATE 42P01)")

	tests := []struct {
		name  string
		errs  []error
		want  error
		calls int
	}{
		{name: "success", errs: []error{nil}, want: nil, calls: 1},
		{name: "deadlock then success", errs: []error{deadlock, nil}, want: nil, calls: 2},
		{name: "deadlock twice", errs: []error{deadlock, deadlock}, want: deadlock, calls: 2},
		{name: "other error", errs: []error{other}, want: other, calls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			err := retryOnConflict(func() error {
				calls++
				return tt.errs[calls-1]
			})

			if !errors.Is(err, tt.want) || calls != tt.calls {
				t.Fatalf("retryOnConflict = %v after %d calls, want %v after %d", err, calls, tt.want, tt.calls)
			}
		})
	}
}

func TestIsConflict(t *testing.T) {
	t.Parallel()

	for msg, want := range map[string]bool{
		"Error 1213 (40001): Deadlock found when trying to get lock":                  true,
		"ERROR: could not serialize access due to concurrent update (SQLSTATE 40001)": true,
		"ERROR: deadlock detected (SQLSTATE 40P01)":                                   true,
		"Error 1062 (23000): Duplicate entry":                                         false,
		"database is locked":                                                          false,
	} {
		if got := isConflict(errors.New(msg)); got != want {
			t.Errorf("isConflict(%q) = %v, want %v", msg, got, want)
		}
	}
}
