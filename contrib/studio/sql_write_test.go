package studio

import (
	"bytes"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var writeTables = append(
	append(fakeCatalog{}, sqlTables...),
	Table{
		Name: "items",
		Columns: []Column{
			{Name: "id", Type: "integer"},
			{Name: "qty", Type: "integer"},
			{Name: "price", Type: "numeric(10,2)", Nullable: true},
			{Name: "active", Type: "boolean", Nullable: true},
			{Name: "born", Type: "date", Nullable: true},
			{Name: "meta", Type: "json", Nullable: true},
			{Name: "note", Type: "text", Nullable: true},
			{Name: "raw", Type: "blob", Nullable: true},
		},
		PrimaryKey: []string{"id"},
	},
	Table{
		Name:       "owners",
		Columns:    []Column{{Name: "id", Type: "integer"}, {Name: "name", Type: "text"}},
		PrimaryKey: []string{"id"},
	},
	Table{
		Name:       "pets",
		Columns:    []Column{{Name: "id", Type: "integer"}, {Name: "owner_id", Type: "integer", Nullable: true}},
		PrimaryKey: []string{"id"},
		ForeignKeys: []ForeignKey{
			{Columns: []string{"owner_id"}, RefTable: "owners", RefColumns: []string{"id"}},
		},
	},
)

// writeStudio is a Studio over the SQL harness plus the tables of writeTables.
func writeStudio(t *testing.T, write bool) (*sqlHarness, *Studio, *bytes.Buffer, *http.Cookie) {
	t.Helper()

	h := newSQLHarness(t)

	for _, q := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, qty INTEGER NOT NULL, price NUMERIC, active BOOLEAN, born DATE,
			meta JSON, note TEXT, raw BLOB)`,
		`INSERT INTO items (id, qty, note, raw) VALUES (1, 5, 'first', x'ff00')`,
		`CREATE TABLE owners (id INTEGER PRIMARY KEY, name TEXT)`,
		`INSERT INTO owners VALUES (1, 'ada')`,
		`CREATE TABLE pets (id INTEGER PRIMARY KEY, owner_id INTEGER REFERENCES owners (id))`,
		`INSERT INTO pets VALUES (1, 1)`,
	} {
		if _, err := h.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}

	h.src = NewSQL("db", "sqlite file", h.db, writeTables)
	h.count.n.Store(0)

	logs := &bytes.Buffer{}

	s, err := New(Config{
		Sources: []Source{h.src}, Version: "t", Write: write, Log: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	return h, s, logs, session(t, s)
}

func (h *sqlHarness) count1(t *testing.T, query string) int {
	t.Helper()

	var n int
	if err := h.db.QueryRowContext(t.Context(), query).Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}

func TestCoerce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		typ  string
		f    Field
		want any
		bad  bool
	}{
		{"integer", Field{Text: " 42 "}, int64(42), false},
		{"bigint", Field{Text: "4x"}, nil, true},
		{"int unsigned", Field{Text: "7"}, int64(7), false},
		{"boolean", Field{Text: "TRUE"}, true, false},
		{"boolean", Field{Text: "0"}, false, false},
		{"boolean", Field{Text: "yes"}, nil, true},
		{"real", Field{Text: "1.5"}, 1.5, false},
		{"numeric(10,2)", Field{Text: "-12.30"}, "-12.30", false},
		{"decimal", Field{Text: "1e5"}, nil, true},
		{"date", Field{Text: "2026-11-11"}, time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC), false},
		{
			"timestamp with time zone",
			Field{Text: "2026-11-11T10:00:00-03:00"},
			time.Date(2026, 11, 11, 13, 0, 0, 0, time.UTC),
			false,
		},
		{"datetime", Field{Text: "tomorrow"}, nil, true},
		{"jsonb", Field{Text: `{"a":1}`}, `{"a":1}`, false},
		{"json", Field{Text: `{a`}, nil, true},
		{"blob", Field{Text: "x"}, nil, true},
		{"varchar(20)", Field{Text: "  keep spaces "}, "  keep spaces ", false},
		{"integer", Field{Null: true}, nil, true},
	}

	for _, tt := range tests {
		got, err := coerce(Column{Name: "c", Type: tt.typ}, tt.f)
		if (err != nil) != tt.bad {
			t.Errorf("coerce(%s, %+v) error = %v, want error %v", tt.typ, tt.f, err, tt.bad)
			continue
		}

		if err != nil && !strings.HasPrefix(err.Error(), "invalid input") {
			t.Errorf("coerce(%s) error %v is not an input error", tt.typ, err)
		}

		if !tt.bad && !cmp.Equal(got, tt.want) {
			t.Errorf("coerce(%s, %+v) = %#v, want %#v", tt.typ, tt.f, got, tt.want)
		}
	}

	if v, err := coerce(Column{Name: "c", Type: "integer", Nullable: true}, Field{Null: true}); err != nil || v != nil {
		t.Errorf("NULL on a nullable column = %v, %v", v, err)
	}
}
