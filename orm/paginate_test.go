package orm

import (
	"context"
	"math"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
)

type countObserver struct{ n atomic.Int32 }

func (o *countObserver) ObserveQuery(context.Context, database.Query) { o.n.Add(1) }

func TestPaginateBeyondLastPage(t *testing.T) {
	t.Parallel()

	db, ctx := setup(t)
	seedUsers(ctx, t, db)

	obs := &countObserver{}
	db.SetQueryObserver(obs)

	tests := []struct {
		name      string
		page      int
		wantItems int
		wantQuery int32
	}{
		{"last page", 2, 1, 2},
		{"beyond", 3, 0, 1},
		{"max int", math.MaxInt, 0, 1},
	}

	for _, tt := range tests {
		obs.n.Store(0)

		p, err := QueryOf[User](db).OrderBy("id", Asc).Paginate(ctx, tt.page, 3)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}

		if len(p.Items) != tt.wantItems || p.Items == nil {
			t.Errorf("%s: items = %v, want %d non-nil", tt.name, p.Items, tt.wantItems)
		}

		if diff := cmp.Diff(
			[]int64{4, 2, int64(tt.page), 3},
			[]int64{p.Total, int64(p.LastPage), int64(p.Page), int64(p.PerPage)},
		); diff != "" {
			t.Errorf("%s: total/last/page/perPage (-want +got):\n%s", tt.name, diff)
		}

		if got := obs.n.Load(); got != tt.wantQuery {
			t.Errorf("%s: statements = %d, want %d", tt.name, got, tt.wantQuery)
		}
	}
}
