package orm

import (
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

func TestWhereRawPostgresLexing(t *testing.T) {
	t.Parallel()

	const prefix = `SELECT "id", "created_at", "updated_at", "name", "email", "age", "active", "settings" FROM "users" WHERE `

	tests := []struct {
		name string
		frag string
		args []any
		want string
	}{
		{"block comment", "age > ? /* why? */", []any{1}, `"name" = $1 AND (age > $2 /* why? */) AND "id" = $3`},
		{
			"line comment with newline",
			"age > ? -- why?\n",
			[]any{1},
			"\"name\" = $1 AND (age > $2 -- why?\n) AND \"id\" = $3",
		},
		{"quoted identifier", `"what?" = ?`, []any{1}, `"name" = $1 AND ("what?" = $2) AND "id" = $3`},
		{
			"dollar body",
			"note = $$?$$ OR age = ?",
			[]any{1},
			`"name" = $1 AND (note = $$?$$ OR age = $2) AND "id" = $3`,
		},
		{
			"escape string",
			`note = E'\'?' OR age = ?`,
			[]any{1},
			`"name" = $1 AND (note = E'\'?' OR age = $2) AND "id" = $3`,
		},
		{
			"jsonb operator",
			"settings::jsonb ?? 'k' AND age = ?",
			[]any{1},
			`"name" = $1 AND (settings::jsonb ? 'k' AND age = $2) AND "id" = $3`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, args, err := QueryOf[User](fakeEx{database.Postgres}).
				Where("name", "a").WhereRaw(tt.frag, tt.args...).Where("id", 9).SQL()
			if err != nil {
				t.Fatal(err)
			}

			if want := prefix + tt.want; s != want || len(args) != 2+len(tt.args) {
				t.Fatalf("SQL() = %q (%d args), want %q (%d args)", s, len(args), want, 2+len(tt.args))
			}
		})
	}
}

func TestWhereRawRejectsUnsafeFragments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    database.Dialect
		frag string
		args []any
		want string
	}{
		{
			"? in comment is not a placeholder",
			database.Postgres,
			"age > 1 /* ? */",
			[]any{1},
			"0 placeholders but 1 args",
		},
		{
			"? in dollar body is not a placeholder",
			database.Postgres,
			"note = $$?$$",
			[]any{1},
			"0 placeholders but 1 args",
		},
		{"?? is literal on postgres", database.Postgres, "settings ?? 'k'", []any{1}, "0 placeholders but 1 args"},
		{"?? is two placeholders on sqlite", database.SQLite, "a = ??", []any{1}, "2 placeholders but 1 args"},
		{"trailing line comment", database.Postgres, "age > ? --", []any{1}, "unterminated"},
		{"unterminated literal", database.Postgres, "name = 'x", nil, "unterminated"},
		{"unterminated block comment", database.SQLite, "age > ? /*", []any{1}, "unterminated"},
		{"unterminated identifier", database.MySQL, "`age > ?", nil, "unterminated"},
		{"explicit $n on postgres", database.Postgres, "age > $1", []any{1}, "$n parameters"},
		{"$n mixed with ? on postgres", database.Postgres, "age > ? AND id = $1", []any{1, 2}, "$n parameters"},
		{"mysql -- without space keeps the placeholder", database.MySQL, "age--?", nil, "1 placeholders but 0 args"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := QueryOf[User](fakeEx{tt.d}).WhereRaw(tt.frag, tt.args...).Where("id", 9).SQL()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("WhereRaw(%q) err = %v, want containing %q", tt.frag, err, tt.want)
			}
		})
	}
}

// A fragment that reaches renumber without WhereRaw's checks still fails
// closed instead of running SQL whose tail is swallowed by a literal.
func TestRenumberRejectsUnterminatedSQL(t *testing.T) {
	t.Parallel()

	for _, d := range []database.Dialect{database.Postgres, database.SQLite, database.MySQL} {
		q := QueryOf[User](fakeEx{d}).addCond(false, "(name = 'x)").Where("id", 9)

		if _, _, err := q.SQL(); err == nil || !strings.Contains(err.Error(), "unterminated") {
			t.Errorf("%s: SQL() err = %v, want an unterminated-SQL error", d.Name(), err)
		}

		if _, err := q.Delete(t.Context()); err == nil || !strings.Contains(err.Error(), "unterminated") {
			t.Errorf("%s: Delete() err = %v, want an unterminated-SQL error", d.Name(), err)
		}
	}
}
