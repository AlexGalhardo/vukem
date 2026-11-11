package orm

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestAggregateTerminals(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)
	books := QueryOf[qaBook](db)

	if got, err := books.Sum[int64](ctx, "views"); err != nil || got != 160 {
		t.Errorf("Sum = %d, %v; want 160 (the trashed row is excluded)", got, err)
	}

	if got, err := books.Where("views", ">", 1000).Sum[int64](ctx, "views"); err != nil || got != 0 {
		t.Errorf("Sum of nothing = %d, %v; want 0", got, err)
	}

	if got, err := books.Avg(ctx, "views"); err != nil || got < 53.3 || got > 53.4 {
		t.Errorf("Avg = %v, %v; want a fraction near 53.33", got, err)
	}

	if got, err := books.Min[int64](ctx, "views"); err != nil || got != 10 {
		t.Errorf("Min = %d, %v", got, err)
	}

	if got, err := books.Max[int64](ctx, "views"); err != nil || got != 100 {
		t.Errorf("Max = %d, %v", got, err)
	}

	none := books.Where("views", ">", 1000)

	if _, err := none.Min[int64](ctx, "views"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Min of nothing = %v, want ErrNotFound", err)
	}

	if _, err := none.Max[int64](ctx, "views"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Max of nothing = %v, want ErrNotFound", err)
	}

	if _, err := none.Avg(ctx, "views"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Avg of nothing = %v, want ErrNotFound", err)
	}
}

func TestDistinct(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	if n, err := QueryOf[qaBook](db).Select("author_id").Distinct().Count(ctx); err != nil || n != 2 {
		t.Errorf("Distinct author_id Count = %d, %v; want 2", n, err)
	}

	ids, err := QueryOf[qaBook](db).Select("author_id").Distinct().OrderBy("author_id", Asc).Pluck[int64](
		ctx,
		"author_id",
	)
	if err != nil || !cmp.Equal(ids, []int64{1, 2}) {
		t.Errorf("Distinct Pluck = %v, %v", ids, err)
	}
}

func TestPaginateJoinDistinctCountsDistinctRows(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	q := QueryOf[qaBook](db).JoinRelation("Reviews").Distinct()

	page, err := q.Paginate(ctx, 1, 2)
	if err != nil {
		t.Fatal(err)
	}

	if page.Total != 3 || len(page.Items) != 2 {
		t.Errorf("Paginate = total %d, %d items; want total 3 (distinct books), 2 items", page.Total, len(page.Items))
	}
}

type authorTotal struct {
	Name  string
	Total int64
	Books int64
}

func TestScan(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	rows, err := QueryOf[qaBook](db).
		InnerJoin[qaAuthor]("qa_authors.id", "=", "qa_books.author_id").
		SelectAs("qa_authors.name", "name").
		SelectExpr(Sum("qa_books.views"), "total").
		SelectExpr(Count("*"), "books").
		GroupBy("qa_authors.name").
		Having(Sum("qa_books.views"), ">", 20).
		OrderBy("qa_authors.name", Asc).
		Scan[authorTotal](ctx)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]authorTotal{{"Ada", 150, 2}}, rows); diff != "" {
		t.Errorf("Scan (-want +got):\n%s", diff)
	}
}

func TestScanErrors(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)
	books := QueryOf[qaBook](db)

	type onlyName struct{ Name string }

	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{"no column list", func() error { _, err := books.Scan[onlyName](ctx); return err }, "explicit column list"},
		{"bad alias", func() error { return books.SelectAs("title", "a b").err }, "invalid alias"},
		{"result column without a field", func() error {
			_, err := books.SelectAs("title", "title").Scan[onlyName](ctx)
			return err
		}, `result column "title" has no field`},
		{"duplicate result column", func() error {
			_, err := books.SelectAs("title", "name").SelectAs("title", "name").Scan[onlyName](ctx)
			return err
		}, "duplicate result column"},
		{"Get on a projected query", func() error {
			_, err := books.SelectAs("title", "name").Get(ctx)
			return err
		}, "use Scan"},
		{"Get on a grouped query", func() error {
			_, err := books.GroupBy("author_id").Get(ctx)
			return err
		}, "use Scan"},
		{"Count on a grouped query", func() error {
			_, err := books.GroupBy("author_id").Count(ctx)
			return err
		}, "does not support GroupBy"},
		{
			"bad Having operator",
			func() error { return books.Having(Sum("views"), "; DROP", 1).err },
			"invalid Having operator",
		},
	}

	for _, tt := range tests {
		if err := tt.run(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want it to contain %q", tt.name, err, tt.want)
		}
	}
}

func TestHavingRaw(t *testing.T) {
	t.Parallel()

	db, ctx := qaSetup(t)

	type perAuthor struct {
		AuthorID int64
		N        int64
	}

	rows, err := QueryOf[qaBook](db).
		SelectAs("author_id", "author_id").SelectExpr(Count("*"), "n").
		GroupBy("author_id").HavingRaw("COUNT(*) >= ?", 2).Scan[perAuthor](ctx)
	if err != nil || !cmp.Equal(rows, []perAuthor{{1, 2}}) {
		t.Errorf("HavingRaw rows = %+v, %v", rows, err)
	}

	if err := QueryOf[qaBook](db).HavingRaw("COUNT(*) >= 'x", 1).err; err == nil {
		t.Error("HavingRaw accepted an unterminated literal")
	}

	if err := QueryOf[qaBook](db).HavingRaw("COUNT(*) >= \x01", 1).err; err == nil {
		t.Error("HavingRaw accepted a reserved control character")
	}
}
