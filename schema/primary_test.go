package schema

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

func TestPrimaryKeyAndGeneratedDDL(t *testing.T) {
	t.Parallel()

	ratings := func(s *Schema) error {
		return s.Create("ratings", func(t *Table) {
			t.ForeignID("user_id")
			t.UUID("item_id")
			t.SmallInteger("story")
			t.SmallInteger("art")
			t.Decimal("overall", 4, 2).StoredAs("(story + art) / 2.0")
			t.Primary("user_id", "item_id")
		})
	}
	items := func(s *Schema) error {
		return s.Create("items", func(t *Table) {
			t.UUID("id").Primary()
			t.String("title")
		})
	}
	addGenerated := func(s *Schema) error {
		return s.Table("ratings", func(t *Table) {
			t.Integer("total").Nullable().StoredAs("story + art")
		})
	}

	tests := []struct {
		name string
		d    database.Dialect
		fn   func(*Schema) error
		want []string
	}{
		{"sqlite composite", database.SQLite, ratings, []string{`CREATE TABLE "ratings" (
  "user_id" INTEGER NOT NULL,
  "item_id" TEXT NOT NULL,
  "story" SMALLINT NOT NULL,
  "art" SMALLINT NOT NULL,
  "overall" NUMERIC(4, 2) GENERATED ALWAYS AS ((story + art) / 2.0) STORED NOT NULL,
  PRIMARY KEY ("user_id", "item_id")
)`}},
		{"postgres composite", database.Postgres, ratings, []string{`CREATE TABLE "ratings" (
  "user_id" BIGINT NOT NULL,
  "item_id" UUID NOT NULL,
  "story" SMALLINT NOT NULL,
  "art" SMALLINT NOT NULL,
  "overall" NUMERIC(4, 2) GENERATED ALWAYS AS ((story + art) / 2.0) STORED NOT NULL,
  PRIMARY KEY ("user_id", "item_id")
)`}},
		{"mysql composite", database.MySQL, ratings, []string{"CREATE TABLE `ratings` (\n" +
			"  `user_id` BIGINT NOT NULL,\n" +
			"  `item_id` CHAR(36) NOT NULL,\n" +
			"  `story` SMALLINT NOT NULL,\n" +
			"  `art` SMALLINT NOT NULL,\n" +
			"  `overall` DECIMAL(4, 2) GENERATED ALWAYS AS ((story + art) / 2.0) STORED NOT NULL,\n" +
			"  PRIMARY KEY (`user_id`, `item_id`)\n)"}},
		{"postgres uuid primary", database.Postgres, items, []string{`CREATE TABLE "items" (
  "id" UUID NOT NULL,
  "title" VARCHAR(255) NOT NULL,
  PRIMARY KEY ("id")
)`}},
		{"postgres add generated", database.Postgres, addGenerated, []string{
			`ALTER TABLE "ratings" ADD COLUMN "total" INTEGER GENERATED ALWAYS AS (story + art) STORED`,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, record(t, tt.d, tt.fn)); diff != "" {
				t.Fatalf("DDL mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPrimaryKeyAndGeneratedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    database.Dialect
		fn   func(*Schema) error
		want string
	}{
		{"primary with ID", database.Postgres, func(s *Schema) error {
			return s.Create("t", func(t *Table) { t.ID(); t.Primary("id") })
		}, "already has a primary key"},
		{"primary twice", database.Postgres, func(s *Schema) error {
			return s.Create("t", func(t *Table) { t.UUID("a").Primary(); t.UUID("b").Primary() })
		}, "already has a primary key"},
		{"primary unknown column", database.Postgres, func(s *Schema) error {
			return s.Create("t", func(t *Table) { t.UUID("a"); t.Primary("a", "missing") })
		}, "unknown column"},
		{"primary on alter", database.Postgres, func(s *Schema) error {
			return s.Table("t", func(t *Table) { t.Primary("a") })
		}, "Create only"},
		{"primary without columns", database.Postgres, func(s *Schema) error {
			return s.Create("t", func(t *Table) { t.UUID("a"); t.Primary() })
		}, "needs columns"},
		{"generated with default", database.Postgres, func(s *Schema) error {
			return s.Create("t", func(t *Table) { t.Integer("a").Default(1).StoredAs("2") })
		}, "cannot have a default"},
		{"generated empty", database.Postgres, func(s *Schema) error {
			return s.Create("t", func(t *Table) { t.Integer("a").StoredAs(" ") })
		}, "needs an expression"},
		{"sqlite add generated", database.SQLite, func(s *Schema) error {
			return s.Table("t", func(t *Table) { t.Integer("a").Nullable().StoredAs("1") })
		}, "sqlite cannot add a stored generated column"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, stmts := recorder(tt.d)

			err := tt.fn(s)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}

			if len(*stmts) != 0 {
				t.Fatalf("statements ran despite the error: %q", *stmts)
			}
		})
	}
}

func TestCompositePrimaryKeyEnforcedOnSQLite(t *testing.T) {
	t.Parallel()

	db := openDB(t)
	s := New(t.Context(), db)

	if err := s.Create("ratings", func(t *Table) {
		t.ForeignID("user_id")
		t.ForeignID("item_id")
		t.SmallInteger("story")
		t.SmallInteger("art")
		t.Integer("total").StoredAs("story + art")
		t.Primary("user_id", "item_id")
	}); err != nil {
		t.Fatal(err)
	}

	insert := "INSERT INTO ratings (user_id, item_id, story, art) VALUES (?, ?, ?, ?)"
	if _, err := db.ExecContext(t.Context(), insert, 1, 1, 3, 4); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(t.Context(), insert, 1, 2, 3, 4); err != nil {
		t.Fatalf("second item for the same user: %v", err)
	}

	if _, err := db.ExecContext(t.Context(), insert, 1, 1, 5, 5); err == nil {
		t.Fatal("duplicate (user_id, item_id) accepted")
	}

	var total int
	if err := db.QueryRowContext(t.Context(), "SELECT total FROM ratings WHERE item_id = 2").Scan(&total); err != nil ||
		total != 7 {
		t.Fatalf("generated total = %d %v, want 7", total, err)
	}
}
