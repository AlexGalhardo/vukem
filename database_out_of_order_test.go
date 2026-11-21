package vukem

import (
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/schema"
)

func TestMigrateRefusesOutOfOrderUnlessAllowed(t *testing.T) {
	t.Parallel()

	id := func(t *schema.Table) { t.ID() }
	a := newAppRun(t, createT("20260101000000_a", "a", id), createT("20260301000000_c", "c", id))

	if out, _, err := a.run(t, "migrate"); err != nil || !strings.Contains(out, "Migrated: 20260301000000_c") {
		t.Fatalf("first migrate = %q, %v", out, err)
	}

	a.migrations = append(a.migrations, createT("20260201000000_b", "b", id))

	_, _, err := a.run(t, "migrate")
	if err == nil || !strings.Contains(err.Error(), "20260201000000_b") ||
		!strings.Contains(err.Error(), "--allow-out-of-order") {
		t.Fatalf("migrate = %v, want the out-of-order refusal naming the migration and the flag", err)
	}

	if out, _, err := a.run(t, "migrate", "-allow-out-of-order"); err != nil ||
		!strings.Contains(out, "Migrated: 20260201000000_b") {
		t.Errorf("migrate -allow-out-of-order = %q, %v", out, err)
	}
}
