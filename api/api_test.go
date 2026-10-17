package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

type Product struct {
	orm.Model

	Name     string
	Category string
	Price    int
	InStock  bool
}

type productJSON struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Price int    `json:"price"`
}

func toJSON(p Product) productJSON { return productJSON{ID: p.ID, Name: p.Name, Price: p.Price} }

func setup(t *testing.T) *database.DB {
	t.Helper()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	ddl := `CREATE TABLE products (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TIMESTAMP, updated_at TIMESTAMP,
		name TEXT NOT NULL, category TEXT NOT NULL, price INTEGER NOT NULL, in_stock BOOLEAN NOT NULL)`
	if _, err := db.ExecContext(t.Context(), ddl); err != nil {
		t.Fatal(err)
	}

	for i, p := range []Product{
		{Name: "Pen", Category: "office", Price: 2, InStock: true},
		{Name: "Desk", Category: "furniture", Price: 300},
		{Name: "Lamp", Category: "furniture", Price: 40, InStock: true},
		{Name: "Book", Category: "books", Price: 15, InStock: true},
		{Name: "Chair", Category: "furniture", Price: 120, InStock: true},
	} {
		if err := orm.Create(t.Context(), db, &p); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	return db
}

// call runs h for target and decodes the JSON body.
func call(t *testing.T, target string, h web.HandlerFunc) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()

	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	r.Header.Set("Accept", "application/json")
	web.NewCtx(rec, r, &web.Services{Log: slog.New(slog.DiscardHandler)}).Serve(h)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: body %q: %v", target, rec.Body.String(), err)
	}

	return rec, body
}

func listHandler(db *database.DB) web.HandlerFunc {
	return func(c *web.Ctx) error {
		req, err := Query(c, orm.QueryOf[Product](db),
			Filters("category", "in_stock"),
			Sorts("name", "price", "id"),
			Fields("id", "name", "price"),
			Includes("reviews"),
			PerPage(2, 3),
		)
		if err != nil {
			return err
		}

		page, err := req.Paginate(c.Context())
		if err != nil {
			return err
		}

		return Page(c, page, toJSON)
	}
}

func names(body map[string]any) []string {
	var out []string
	for _, d := range body["data"].([]any) {
		out = append(out, d.(map[string]any)["name"].(string))
	}

	return out
}

func TestPageEnvelope(t *testing.T) {
	t.Parallel()

	db := setup(t)

	_, body := call(t, "/products?page=2", listHandler(db))
	want := map[string]any{
		"data": []any{
			map[string]any{"id": 3.0, "name": "Lamp", "price": 40.0},
			map[string]any{"id": 4.0, "name": "Book", "price": 15.0},
		},
		"meta":  map[string]any{"page": 2.0, "per_page": 2.0, "total": 5.0, "last_page": 3.0},
		"links": map[string]any{"self": "/products?page=2", "next": "/products?page=3", "prev": "/products?page=1"},
	}

	if diff := cmp.Diff(want, body); diff != "" {
		t.Fatalf("page envelope (-want +got):\n%s", diff)
	}

	_, first := call(t, "/products?per_page=50", listHandler(db))
	links := first["links"].(map[string]any)
	if first["meta"].(map[string]any)["per_page"] != 3.0 || links["prev"] != nil || links["next"] == nil {
		t.Fatalf("first page = %v", first)
	}
}

func TestQueryConventions(t *testing.T) {
	t.Parallel()

	db := setup(t)

	tests := []struct {
		target string
		want   []string
	}{
		{"/p?filter[category]=furniture&sort=-price", []string{"Desk", "Chair"}},
		{"/p?filter[category]=furniture,books&sort=name", []string{"Book", "Chair"}},
		{"/p?filter[in_stock]=false", []string{"Desk"}},
		{"/p?filter[in_stock]=true&sort=price&per_page=3", []string{"Pen", "Book", "Lamp"}},
		{"/p?sort=-price,name&fields=id,name", []string{"Desk", "Chair"}},
		{"/p?include=reviews&page=3", []string{"Chair"}},
		// SQL in filter values is bound, never executed.
		{"/p?filter[category]=furniture'%20OR%20'1'='1", nil},
	}

	for _, tt := range tests {
		rec, body := call(t, tt.target, listHandler(db))
		if rec.Code != 200 {
			t.Errorf("%s = %d %v", tt.target, rec.Code, body)
			continue
		}

		if diff := cmp.Diff(tt.want, names(body)); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tt.target, diff)
		}
	}
}

func TestSecurity_A05_QueryConventionsRejectUnknownColumns(t *testing.T) {
	t.Parallel()

	db := setup(t)

	for _, target := range []string{
		"/p?sort=password",
		"/p?sort=name%3BDROP%20TABLE%20products",
		"/p?sort=-name)--",
		"/p?filter[secret]=1",
		"/p?filter[category=1",
		"/p?fields=id,password",
		"/p?include=users",
	} {
		rec, body := call(t, target, listHandler(db))
		if rec.Code != 400 || !strings.Contains(body["detail"].(string), "Allowed:") {
			t.Errorf("%s = %d %v", target, rec.Code, body)
		}
	}

	// The table is intact.
	if n, err := orm.QueryOf[Product](db).Count(t.Context()); err != nil || n != 5 {
		t.Fatalf("products = %d %v", n, err)
	}

	rec, body := call(t, "/p?filter[x]=1", func(c *web.Ctx) error {
		_, err := Query(c, orm.QueryOf[Product](db))
		return err
	})
	if rec.Code != 400 || !strings.Contains(body["detail"].(string), "Allowed: none") {
		t.Fatalf("no allowlist = %d %v", rec.Code, body)
	}
}

func TestCursorAndCollection(t *testing.T) {
	t.Parallel()

	db := setup(t)
	h := func(c *web.Ctx) error {
		req, err := Query(c, orm.QueryOf[Product](db), PerPage(2, 10))
		if err != nil {
			return err
		}

		page, err := req.Query.CursorPaginate(c.Context(), req.Cursor, req.PerPage, orm.Asc)
		if err != nil {
			return err
		}

		return Cursor(c, page, toJSON)
	}

	var seen []string

	target := "/p"
	for range 5 {
		_, body := call(t, target, h)
		seen = append(seen, names(body)...)

		next, _ := body["links"].(map[string]any)["next"].(string)
		if next == "" {
			if body["meta"].(map[string]any)["next_cursor"] != nil {
				t.Fatal("last page has a cursor")
			}

			break
		}

		target = next
	}

	if diff := cmp.Diff([]string{"Pen", "Desk", "Lamp", "Book", "Chair"}, seen); diff != "" {
		t.Fatalf("cursor walk (-want +got):\n%s", diff)
	}

	if rec, _ := call(t, "/p?cursor=not-base64!", h); rec.Code != 400 {
		t.Fatalf("bad cursor = %d", rec.Code)
	}

	_, body := call(t, "/p", func(c *web.Ctx) error {
		ps, err := orm.QueryOf[Product](db).Where("price", ">", 100).Get(c.Context())
		if err != nil {
			return err
		}

		return Collection(c, ps, toJSON)
	})
	if diff := cmp.Diff([]string{"Desk", "Chair"}, names(body)); diff != "" || body["meta"] != nil {
		t.Fatalf("collection = %v", body)
	}
}

func TestDeprecate(t *testing.T) {
	t.Parallel()

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sunset := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	rec := httptest.NewRecorder()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	Deprecate(
		since,
		sunset,
		"/docs/v2",
	)(
		ok,
	).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody))

	h := rec.Header()
	if h.Get("Deprecation") != "@1767225600" || h.Get("Sunset") != "Fri, 01 Jan 2027 00:00:00 GMT" ||
		h.Get("Link") != `</docs/v2>; rel="deprecation"; type="text/html"` || rec.Code != http.StatusNoContent {
		t.Fatalf("headers = %v", h)
	}

	rec = httptest.NewRecorder()
	Deprecate(
		since,
		time.Time{},
		"",
	)(
		ok,
	).ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody))
	if rec.Header().Get("Sunset") != "" || rec.Header().Get("Link") != "" {
		t.Fatal("optional headers set")
	}
}

func TestSecurity_A04_QueryBoundsPageAndFilterLists(t *testing.T) {
	t.Parallel()

	db := setup(t)
	long := strings.TrimSuffix(strings.Repeat("x,", 101), ",")

	tests := []struct {
		target string
		code   int
		detail string
	}{
		{"/p?page=1000", 200, ""},
		{"/p?page=1001", 400, "page"},
		{"/p?page=99999999999", 400, "page"},
		{"/p?filter[category]=" + long, 400, "filter[category]"},
		{"/p?filter[category]=" + strings.TrimSuffix(strings.Repeat("x,", 100), ","), 200, ""},
	}

	for _, tt := range tests {
		rec, body := call(t, tt.target, listHandler(db))
		if rec.Code != tt.code {
			t.Errorf("%.60s = %d %v, want %d", tt.target, rec.Code, body, tt.code)
			continue
		}

		if tt.detail != "" && !strings.Contains(body["detail"].(string), tt.detail) {
			t.Errorf("%.60s detail = %q, want it to name %q", tt.target, body["detail"], tt.detail)
		}
	}

	custom := func(c *web.Ctx) error {
		_, err := Query(c, orm.QueryOf[Product](db), Filters("category"), MaxPage(5), MaxFilterValues(2))
		return err
	}

	for target, want := range map[string]int{
		"/p?page=5": 200, "/p?page=6": 400, "/p?filter[category]=a,b": 200, "/p?filter[category]=a,b,c": 400,
	} {
		if rec, body := call(t, target, func(c *web.Ctx) error {
			if err := custom(c); err != nil {
				return err
			}

			return c.JSON(200, map[string]any{})
		}); rec.Code != want {
			t.Errorf("custom limits %s = %d %v, want %d", target, rec.Code, body, want)
		}
	}
}

func TestQueryLimitsDefaultWhenNotPositive(t *testing.T) {
	t.Parallel()

	db := setup(t)

	for _, n := range []int{0, -1} {
		handler := func(c *web.Ctx) error {
			if _, err := Query(
				c,
				orm.QueryOf[Product](db),
				Filters("category"),
				MaxPage(n),
				MaxFilterValues(n),
			); err != nil {
				return err
			}

			return c.JSON(200, map[string]any{})
		}

		for target, want := range map[string]int{
			"/p?page=2": 200, "/p?page=1000": 200, "/p?page=1001": 400,
			"/p?filter[category]=a,b": 200,
		} {
			if rec, body := call(t, target, handler); rec.Code != want {
				t.Errorf("limits %d: %s = %d %v, want %d", n, target, rec.Code, body, want)
			}
		}
	}
}
