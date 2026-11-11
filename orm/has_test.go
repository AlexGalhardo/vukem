package orm

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestWhereHas(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	tests := []struct {
		name string
		q    Query[qaBook]
		want []string
	}{
		{
			"has many", QueryOf[qaBook](db).WhereHas[qaReview]("Reviews", func(q Query[qaReview]) Query[qaReview] {
				return q.Where("body", "great")
			}), []string{"Go"},
		},
		{
			"has many without a filter",
			QueryOf[qaBook](db).WhereHas[qaReview]("Reviews", nil),
			[]string{"Go", "SQL", "Zig"},
		},
		{
			"many to many", QueryOf[qaBook](db).WhereHas[qaTag]("Tags", func(q Query[qaTag]) Query[qaTag] {
				return q.Where("label", "a")
			}), []string{"Go"},
		},
		{"many to many, any", QueryOf[qaBook](db).WhereHas[qaTag]("Tags", nil), []string{"Go", "SQL"}},
		{"belongs to", QueryOf[qaBook](db).WhereHas[qaAuthor]("Author", func(q Query[qaAuthor]) Query[qaAuthor] {
			return q.Where("name", "Bob")
		}), []string{"Zig"}},
		{"does not have", QueryOf[qaBook](db).WhereDoesntHave[qaTag]("Tags", nil), []string{"Zig"}},
		{
			"an OR filter inside fn stays inside the correlation",
			QueryOf[qaBook](db).WhereHas[qaReview]("Reviews", func(q Query[qaReview]) Query[qaReview] {
				return q.Where("body", "good").OrWhere("body", "fine")
			}),
			[]string{"Go", "Zig"},
		},
	}

	for _, tt := range tests {
		got, err := tt.q.OrderBy("id", Asc).Get(ctx)
		if err != nil || !cmp.Equal(titles(got), tt.want) {
			t.Errorf("%s = %v, %v; want %v", tt.name, titles(got), err, tt.want)
		}
	}

	// The soft delete of the related model applies inside the EXISTS.
	authors, err := QueryOf[qaAuthor](db).WhereHas[qaBook]("Books", func(q Query[qaBook]) Query[qaBook] {
		return q.Where("title", "Old")
	}).Get(ctx)
	if err != nil || len(authors) != 0 {
		t.Errorf("WhereHas on a trashed related row = %+v, %v; want none", authors, err)
	}
}

func TestWhereHasErrors(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	tests := []struct {
		name string
		q    Query[qaBook]
		want string
	}{
		{"unknown relation", QueryOf[qaBook](db).WhereHas[qaReview]("Nope", nil), `no relation "Nope"`},
		{"wrong type", QueryOf[qaBook](db).WhereHas[qaTag]("Reviews", nil), `is orm.qaReview, not orm.qaTag`},
	}

	for _, tt := range tests {
		if _, err := tt.q.Get(ctx); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want it to contain %q", tt.name, err, tt.want)
		}
	}
}

func TestWithCount(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	counter := &countEx{Executor: db}

	got, err := QueryOf[qaBook](counter).WithCount("Reviews", "Tags").OrderBy("id", Asc).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var counts [][2]int64
	for _, b := range got {
		counts = append(counts, [2]int64{b.ReviewCount, b.TagCount})
	}

	if diff := cmp.Diff([][2]int64{{2, 2}, {1, 1}, {1, 0}}, counts); diff != "" {
		t.Errorf("counts (-want +got):\n%s", diff)
	}

	// One query for the rows and one per relation counted.
	if counter.n != 3 {
		t.Errorf("queries = %d, want 3", counter.n)
	}

	if _, err := QueryOf[qaBook](
		db,
	).WithCount("Author").
		Get(ctx); err == nil ||
		!strings.Contains(err.Error(), "WithCount supports") {
		t.Errorf("WithCount(belongs to) = %v", err)
	}

	if _, err := QueryOf[qaAuthor](db).WithCount("Books").Get(ctx); err == nil ||
		!strings.Contains(err.Error(), `tagged orm:"count:Books"`) {
		t.Errorf("WithCount without a tagged field = %v", err)
	}
}
