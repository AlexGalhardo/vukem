package database

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// bareDialect implements Dialect only, like a third-party dialect written
// before Capabilities existed.
type bareDialect struct{}

func (bareDialect) Name() string           { return "bare" }
func (bareDialect) Placeholder(int) string { return "?" }
func (bareDialect) Quote(s string) string  { return s }

type liteLike struct{ sqliteDialect }

func (liteLike) Name() string { return "turso" }

func TestCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		d      Dialect
		want   Capabilities
		wantOK bool
	}{
		{"sqlite", SQLite, Capabilities{
			Family: "sqlite", Returning: true, MaxParams: 32766, LimitForOffset: "-1",
			TransactionalDDL: true, PartialIndexes: true,
		}, true},
		{"postgres", Postgres, Capabilities{
			Family: "postgres", Returning: true, MaxParams: 65535, RowLocks: true, ILike: true,
			TransactionalDDL: true, AdvisoryLock: "postgres", PartialIndexes: true, IndexMethods: true,
			AlterConstraints: true,
		}, true},
		{"mysql", MySQL, Capabilities{
			Family: "mysql", MaxParams: 65535, RowLocks: true, LimitForOffset: "18446744073709551615",
			AdvisoryLock: "mysql", AlterConstraints: true,
		}, true},
		{"third party that describes itself", liteLike{}, SQLite.(Capable).Capabilities(), true},
		{"dialect without capabilities", bareDialect{}, Capabilities{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := CapabilitiesOf(tt.d)
			if ok != tt.wantOK {
				t.Fatalf("CapabilitiesOf ok = %v, want %v", ok, tt.wantOK)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("capabilities (-want +got):\n%s", diff)
			}
		})
	}
}
