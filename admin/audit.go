package admin

import (
	"context"
	"reflect"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/schema"
)

// AuditTable is the table holding the admin audit log.
const AuditTable = "admin_audit_logs"

// Audit actions.
const (
	ActionCreate = "create"
	ActionUpdate = "update"
	ActionDelete = "delete"
)

// AuditLog is one change made through the admin panel.
type AuditLog struct {
	ID int64 `db:"id"`
	// ActorID is the AuthID of the user who made the change.
	ActorID string `db:"actor_id"`
	// Resource is the resource slug ("posts").
	Resource string `db:"resource"`
	// RecordID is the primary key of the changed record.
	RecordID string `db:"record_id"`
	// Action is ActionCreate, ActionUpdate or ActionDelete.
	Action string `db:"action"`
	// Changes maps changed columns to their values before and after.
	// Sensitive columns only record that they changed.
	Changes   orm.JSON[map[string]Change] `db:"changes"`
	CreatedAt time.Time                   `db:"created_at"`
}

// Table implements orm.Tabler.
func (AuditLog) Table() string { return AuditTable }

// Change is the before/after value of one column. Redacted changes (to
// sensitive columns) carry no values.
type Change struct {
	Before   any  `json:"before,omitempty"`
	After    any  `json:"after,omitempty"`
	Redacted bool `json:"redacted,omitempty"`
}

// Migration creates the audit log table; register it with a timestamped
// id (vukem install:admin does):
//
//	admin.Migration("20261016120000_create_admin_audit_logs_table")
func Migration(id string) schema.Migration {
	return schema.Migration{
		ID: id,
		Up: func(s *schema.Schema) error {
			return s.Create(AuditTable, func(t *schema.Table) {
				t.ID()
				t.String("actor_id")
				t.String("resource", 100)
				t.String("record_id")
				t.String("action", 16)
				t.Text("changes")
				t.Timestamp("created_at").Index()
				t.Index("resource", "record_id")
			})
		},
		Down: func(s *schema.Schema) error { return s.DropIfExists(AuditTable) },
	}
}

// snapshot captures the auditable values of rec's columns.
func (b *base) snapshot(rec reflect.Value, cols []string) map[string]any {
	out := make(map[string]any, len(cols))
	for _, c := range cols {
		out[c] = auditValue(rec.FieldByIndex(b.byName[c].index))
	}

	return out
}

// diff lists the columns whose values differ; sensitive columns are
// recorded as redacted, without values (A09: never log secrets).
func (b *base) diff(before, after map[string]any, cols []string) map[string]Change {
	out := map[string]Change{}

	for _, c := range cols {
		bv, av := before[c], after[c]
		if reflect.DeepEqual(bv, av) {
			continue
		}

		if b.sensitive[c] {
			out[c] = Change{Redacted: true}
			continue
		}

		out[c] = Change{Before: bv, After: av}
	}

	return out
}

func writeAudit(ctx context.Context, ex database.Executor, entry *AuditLog) error {
	return orm.Create(ctx, ex, entry)
}
