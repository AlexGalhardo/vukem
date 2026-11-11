package orm

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

// countEx counts the statements run through it.
type countEx struct {
	database.Executor

	n int
}

func (c *countEx) ExecContext(ctx context.Context, q string, a ...any) (sql.Result, error) {
	c.n++
	return c.Executor.ExecContext(ctx, q, a...)
}

func (c *countEx) QueryContext(ctx context.Context, q string, a ...any) (*sql.Rows, error) {
	c.n++
	return c.Executor.QueryContext(ctx, q, a...)
}

func (c *countEx) QueryRowContext(ctx context.Context, q string, a ...any) *sql.Row {
	c.n++
	return c.Executor.QueryRowContext(ctx, q, a...)
}

func TestNestedWith(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)
	counter := &countEx{Executor: db}

	books, err := QueryOf[qaBook](counter).With("Reviews.Reviewer").OrderBy("id", Asc).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Books, their reviews, the reviewers of every loaded review.
	if counter.n != 3 {
		t.Errorf("queries = %d, want 3", counter.n)
	}

	var seen []string

	for _, b := range books {
		revs, err := b.Reviews.Get()
		if err != nil {
			t.Fatal(err)
		}

		for _, r := range revs {
			who, err := r.Reviewer.Get()
			if err != nil || who == nil {
				t.Fatalf("reviewer of %q not loaded: %v, %v", r.Body, who, err)
			}

			seen = append(seen, b.Title+":"+who.Name)
		}
	}

	if diff := cmp.Diff([]string{"Go:Bob", "Go:Cy", "SQL:Cy", "Zig:Ada"}, seen); diff != "" {
		t.Errorf("nested reviewers (-want +got):\n%s", diff)
	}

	// The first level is not loaded twice when named as well.
	counter.n = 0

	if _, err := QueryOf[qaBook](counter).With("Reviews", "Reviews.Reviewer").Get(ctx); err != nil || counter.n != 3 {
		t.Errorf("With(Reviews, Reviews.Reviewer) = %v, %d queries; want 3", err, counter.n)
	}

	if err := QueryOf[qaBook](
		db,
	).With("Reviews.Nope").
		err; err == nil ||
		!strings.Contains(err.Error(), `no relation "Nope"`) {
		t.Errorf("With(unknown nested) = %v", err)
	}
}

func TestWithWhere(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	books, err := QueryOf[qaBook](db).
		WithWhere[qaReview]("Reviews", func(q Query[qaReview]) Query[qaReview] {
		return q.Where("body", "!=", "").OrderBy("id", Desc)
	}).
		OrderBy("id", Asc).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var bodies [][]string

	for _, b := range books {
		revs, _ := b.Reviews.Get()

		var bs []string
		for _, r := range revs {
			bs = append(bs, r.Body)
		}

		bodies = append(bodies, bs)
	}

	if diff := cmp.Diff([][]string{{"great", "good"}, nil, {"fine"}}, bodies); diff != "" {
		t.Errorf("restricted reviews (-want +got):\n%s", diff)
	}

	for name, fn := range map[string]func(Query[qaReview]) Query[qaReview]{
		"limit":  func(q Query[qaReview]) Query[qaReview] { return q.Limit(1) },
		"offset": func(q Query[qaReview]) Query[qaReview] { return q.Offset(1) },
		"select": func(q Query[qaReview]) Query[qaReview] { return q.Select("id") },
	} {
		err := QueryOf[qaBook](db).WithWhere("Reviews", fn).err
		if err == nil || !strings.Contains(err.Error(), "supports Where and OrderBy only") {
			t.Errorf("WithWhere with %s = %v", name, err)
		}
	}

	if err := QueryOf[qaBook](db).WithWhere[qaTag]("Reviews", nil).err; err == nil {
		t.Error("WithWhere accepted the wrong model type")
	}
}

func TestWithWhereOrdersManyToMany(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	books, err := QueryOf[qaBook](db).
		WithWhere[qaTag]("Tags", func(q Query[qaTag]) Query[qaTag] { return q.OrderBy("label", Desc) }).
		OrderBy("id", Asc).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	tags, _ := books[0].Tags.Get()

	var labels []string
	for _, tg := range tags {
		labels = append(labels, tg.Label)
	}

	if diff := cmp.Diff([]string{"b", "a"}, labels); diff != "" {
		t.Errorf("tags of the first book (-want +got):\n%s", diff)
	}
}

func TestWithWhereTwiceOnOneRelationIsAnError(t *testing.T) {
	t.Parallel()

	db, _ := qaSetup(t)
	fn := func(q Query[qaReview]) Query[qaReview] { return q }

	err := QueryOf[qaBook](db).WithWhere("Reviews", fn).WithWhere("Reviews", fn).err
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Errorf("two WithWhere on Reviews = %v, want an error", err)
	}
}

func TestHavingRawWithoutExecutor(t *testing.T) {
	t.Parallel()

	q := Query[qaBook]{m: metaOf[qaBook](), limit: -1}
	if err := q.HavingRaw("COUNT(*) > ?", 1).err; err != nil {
		t.Errorf("HavingRaw without an executor = %v, want it to lex with SQLite rules", err)
	}
}
