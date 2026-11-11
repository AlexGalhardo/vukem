package schema

import (
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

func mig(id string, up func(s *Schema) error) Migration { return Migration{ID: id, Up: up} }

func createPosts(s *Schema) error {
	return s.Create("posts", func(t *Table) {
		t.ID()
		t.String("title")
	})
}

func addSlug(s *Schema) error { return s.Table("posts", func(t *Table) { t.String("slug") }) }

func alterPosts(fn func(t *Table)) func(s *Schema) error {
	return func(s *Schema) error { return s.Table("posts", fn) }
}

type conflictCase struct {
	name string
	migs []Migration
	want []string // "<id> <table>.<column>: <message>"
}

func TestFindConflicts(t *testing.T) {
	t.Parallel()

	tests := []conflictCase{
		{name: "valid sequence", migs: []Migration{mig("1", createPosts), mig("2", addSlug)}},
		{
			name: "table created twice",
			migs: []Migration{mig("1", createPosts), mig("2", createPosts)},
			want: []string{"2 posts.: table already created by 1"},
		},
		{
			name: "two branches add the same column",
			migs: []Migration{mig("1", createPosts), mig("2", addSlug), mig("3", addSlug)},
			want: []string{"3 posts.slug: column already added by 2"},
		},
		{
			name: "drop of a missing column",
			migs: []Migration{mig("1", createPosts), mig("2", alterPosts(func(t *Table) { t.DropColumn("nope") }))},
			want: []string{"2 posts.nope: column does not exist, so it cannot be dropped"},
		},
		{
			name: "rename of a missing column",
			migs: []Migration{
				mig("1", createPosts),
				mig("2", alterPosts(func(t *Table) { t.RenameColumn("nope", "x") })),
			},
			want: []string{"2 posts.nope: column does not exist, so it cannot be renamed to x"},
		},
		{
			name: "alter before create",
			migs: []Migration{mig("1", addSlug), mig("2", createPosts)},
			want: []string{"1 posts.: table does not exist at this point"},
		},
		{
			name: "drop before create",
			migs: []Migration{mig("1", func(s *Schema) error { return s.Drop("posts") })},
			want: []string{"1 posts.: table does not exist, so it cannot be dropped"},
		},
		{
			name: "index name used twice",
			migs: []Migration{
				mig("1", func(s *Schema) error {
					return s.Create("posts", func(t *Table) {
						t.ID()
						t.String("title")
						t.Index("title")
					})
				}),
				mig("2", alterPosts(func(t *Table) { t.Index("title") })),
			},
			want: []string{"2 posts.: index posts_title_index already created by 1"},
		},
		{
			name: "DropIfExists of an absent table is fine",
			migs: []Migration{mig("1", func(s *Schema) error { return s.DropIfExists("posts") })},
		},
		{
			name: "Raw is opaque",
			migs: []Migration{
				mig("1", func(s *Schema) error { return s.Raw("CREATE TABLE posts (id INT)") }),
				mig("2", createPosts),
			},
		},
		{
			name: "a rename carries the columns",
			migs: []Migration{
				mig("1", createPosts),
				mig("2", func(s *Schema) error { return s.Rename("posts", "articles") }),
				mig(
					"3",
					func(s *Schema) error { return s.Table("articles", func(t *Table) { t.DropColumn("title") }) },
				),
			},
		},
		{
			name: "Rebuild replaces the columns",
			migs: []Migration{
				mig("1", createPosts),
				mig("2", func(s *Schema) error { return s.Rebuild("posts", func(t *Table) { t.ID() }) }),
				mig("3", alterPosts(func(t *Table) { t.DropColumn("title") })),
			},
			want: []string{"3 posts.title: column does not exist, so it cannot be dropped"},
		},
	}

	for _, tt := range tests {
		got, err := FindConflicts(database.SQLite, tt.migs)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}

		if len(got) != len(tt.want) {
			t.Errorf("%s: %d conflicts %+v, want %d", tt.name, len(got), got, len(tt.want))
			continue
		}

		for i, w := range tt.want {
			c := got[i]
			if line := c.ID + " " + c.Table + "." + c.Column + ": " + c.Message; line != w {
				t.Errorf("%s: conflict %q, want %q", tt.name, line, w)
			}
		}
	}
}

func TestFindConflictsChangeOfAMissingColumn(t *testing.T) {
	t.Parallel()

	got, err := FindConflicts(database.Postgres, []Migration{
		mig("1", createPosts),
		mig("2", alterPosts(func(t *Table) { t.String("nope", 10).Change() })),
	})
	if err != nil || len(got) != 1 || got[0].Column != "nope" ||
		!strings.Contains(got[0].Message, "cannot be changed") {
		t.Errorf("FindConflicts = %+v, %v", got, err)
	}
}

func TestFindConflictsReportsABuilderError(t *testing.T) {
	t.Parallel()

	_, err := FindConflicts(database.SQLite, []Migration{mig("1", func(s *Schema) error {
		return s.Create("bad name", func(t *Table) { t.ID() })
	})})
	if err == nil || !strings.Contains(err.Error(), "migration 1") {
		t.Errorf("FindConflicts with a builder error = %v", err)
	}
}
