package schema

import (
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

func TestIndexNameLetsTwoPartialIndexesShareColumns(t *testing.T) {
	t.Parallel()

	ddl := strings.Join(record(t, database.Postgres, func(s *Schema) error {
		return s.Create("keys", func(t *Table) {
			t.String("owner")
			t.String("state")
			t.UniqueIndex("owner").Name("keys_live_owner").Where("state = 'live'")
			t.UniqueIndex("owner").Name("keys_draft_owner").Where("state = 'draft'")
		})
	}), "\n")

	for _, want := range []string{`"keys_live_owner"`, `"keys_draft_owner"`} {
		if !strings.Contains(ddl, want) {
			t.Errorf("DDL = %s, want it to contain %s", ddl, want)
		}
	}

	if strings.Contains(ddl, "keys_owner_unique") {
		t.Errorf("DDL = %s, want the derived name replaced", ddl)
	}
}

func TestIndexNameRefusesAnUnsafeName(t *testing.T) {
	t.Parallel()

	s, _ := recorder(database.Postgres)

	err := s.Create("keys", func(t *Table) {
		t.String("owner")
		t.Index("owner").Name(`x"; DROP TABLE keys; --`)
	})
	if err == nil {
		t.Fatal("Create() with an unsafe index name = nil, want an error")
	}
}

func TestReferencesPointsTheForeignKeyAtAnotherColumn(t *testing.T) {
	t.Parallel()

	ddl := strings.Join(record(t, database.Postgres, func(s *Schema) error {
		return s.Create("items", func(t *Table) {
			t.ForeignID("owner_id").Constrained("owners").References("public_id")
		})
	}), "\n")

	if !strings.Contains(ddl, `REFERENCES "owners" ("public_id")`) {
		t.Errorf("DDL = %s, want a reference to owners(public_id)", ddl)
	}
}

func TestReferencesNeedsConstrainedFirst(t *testing.T) {
	t.Parallel()

	s, _ := recorder(database.Postgres)

	err := s.Create("items", func(t *Table) { t.ForeignID("owner_id").References("public_id") })
	if err == nil || !strings.Contains(err.Error(), "Constrained") {
		t.Fatalf("References() without Constrained() = %v, want an error naming Constrained", err)
	}
}
