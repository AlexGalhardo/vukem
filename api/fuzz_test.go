package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// FuzzQuery feeds the raw query string to Query, which interprets
// ?filter, ?sort, ?fields, ?include, ?page, ?per_page and ?cursor from the
// client: it must never panic, and an accepted query must build SQL.
func FuzzQuery(f *testing.F) {
	for _, s := range []string{
		"filter[category]=furniture", "filter[category]=a,b&sort=-price", "sort=-id", "sort=name,-price",
		"include=reviews", "fields=id,name", "page=1&per_page=5", "page=-1&per_page=0", "cursor=abc",
		"filter[]=x", "filter[category][x]=1", "sort=,", "fields=,,", "include=reviews,reviews",
		"filter[category]=" + string(make([]byte, 8)), "%zz", "&&&", "=",
	} {
		f.Add(s)
	}

	db, err := database.Open(context.Background(), ":memory:")
	if err != nil {
		f.Fatal(err)
	}

	f.Cleanup(func() { _ = db.Close() })

	svc := &web.Services{Log: slog.New(slog.DiscardHandler)}

	f.Fuzz(func(t *testing.T, rawQuery string) {
		r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		r.URL.RawQuery = rawQuery

		req, err := Query(web.NewCtx(httptest.NewRecorder(), r, svc), orm.QueryOf[Product](db),
			Filters("category", "in_stock"),
			Sorts("name", "price", "id"),
			Fields("id", "name", "price"),
			Includes("reviews"),
		)
		if err != nil {
			return
		}

		if _, _, err := req.Query.SQL(); err != nil {
			t.Fatalf("Query(%q) accepted, but its SQL fails: %v", rawQuery, err)
		}
	})
}
