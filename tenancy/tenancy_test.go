package tenancy_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/tenancy"
	"github.com/AlexGalhardo/vukem/web"
)

type Task struct {
	orm.Model
	WorkspaceID string
	Title       string
}

func (t *Task) SetTenant(id tenancy.ID) { t.WorkspaceID = string(id) }

func (t *Task) BeforeCreate(ctx context.Context) error { return tenancy.Assign(ctx, t) }

type user struct{ id string }

func (u user) AuthID() string       { return u.id }
func (u user) AuthPassword() string { return "" }

func newDB(t *testing.T) *database.DB {
	t.Helper()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	_, err = db.ExecContext(t.Context(), `CREATE TABLE tasks (id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TIMESTAMP, updated_at TIMESTAMP, workspace_id TEXT NOT NULL, title TEXT)`)
	if err != nil {
		t.Fatal(err)
	}

	return db
}

func seed(t *testing.T, db *database.DB) {
	t.Helper()

	for ws, title := range map[tenancy.ID]string{"w1": "mine", "w2": "theirs"} {
		task := Task{Title: title}
		if err := orm.Create(tenancy.With(t.Context(), ws), db, &task); err != nil {
			t.Fatal(err)
		}
	}
}

func titles(ctx context.Context, t *testing.T, db *database.DB) []string {
	t.Helper()

	rows, err := orm.QueryOf[Task](db).Scope(tenancy.Scope[Task](ctx, "workspace_id")).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var out []string
	for _, r := range rows {
		out = append(out, r.Title)
	}

	return out
}

func TestSecurity_A01_CrossTenantReadReturnsNothing(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	seed(t, db)

	got := titles(tenancy.With(t.Context(), "w1"), t, db)
	if len(got) != 1 || got[0] != "mine" {
		t.Errorf("w1 sees %v, want [mine]", got)
	}

	if got := titles(tenancy.With(t.Context(), "w3"), t, db); len(got) != 0 {
		t.Errorf("w3 sees %v, want nothing", got)
	}
}

func TestSecurity_A01_MissingTenantScopeMatchesNothing(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	seed(t, db)

	for _, ctx := range []context.Context{t.Context(), tenancy.With(t.Context(), "")} {
		if got := titles(ctx, t, db); len(got) != 0 {
			t.Errorf("no tenant sees %v, want nothing", got)
		}
	}
}

func TestSecurity_A01_InsertTakesTheContextTenant(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	task := Task{WorkspaceID: "w2", Title: "forged"}

	if err := orm.Create(tenancy.With(t.Context(), "w1"), db, &task); err != nil {
		t.Fatal(err)
	}

	if task.WorkspaceID != "w1" {
		t.Errorf("WorkspaceID = %q, want w1 (input must not win)", task.WorkspaceID)
	}

	if got := titles(tenancy.With(t.Context(), "w2"), t, db); len(got) != 0 {
		t.Errorf("w2 sees the forged row: %v", got)
	}

	err := orm.Create(t.Context(), db, &Task{Title: "orphan", WorkspaceID: "w2"})
	if !errors.Is(err, tenancy.ErrNoTenant) {
		t.Errorf("insert without tenant = %v, want ErrNoTenant", err)
	}
}

// asUser mounts what auth.Required does on success: the principal.
func asUser(id string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if id != "" {
				c := web.NewCtx(w, r, web.ServicesFrom(r.Context()))
				auth.SetUser(c, "web", user{id})
				r = c.Request()
			}

			next.ServeHTTP(w, r)
		})
	}
}

func serve(t *testing.T, userID string, cfg tenancy.Config, path string) (*httptest.ResponseRecorder, tenancy.ID) {
	t.Helper()

	var seen tenancy.ID

	mux := http.NewServeMux()
	mux.Handle("GET /w/{ws}", asUser(userID)(tenancy.Middleware(cfg)(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			seen, _ = tenancy.From(r.Context())
		}))))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

	return rec, seen
}

func members(ok map[string]bool) func(context.Context, string, tenancy.ID) (bool, error) {
	return func(_ context.Context, u string, id tenancy.ID) (bool, error) { return ok[u+"/"+string(id)], nil }
}

func TestSecurity_A01_CrossTenantPathIs404(t *testing.T) {
	t.Parallel()

	cfg := tenancy.Config{Resolve: tenancy.PathValue("ws"), Member: members(map[string]bool{"7/w1": true})}

	if rec, seen := serve(t, "7", cfg, "/w/w1"); rec.Code != http.StatusOK || seen != "w1" {
		t.Errorf("member: status %d, tenant %q", rec.Code, seen)
	}

	if rec, seen := serve(t, "7", cfg, "/w/w2"); rec.Code != http.StatusNotFound || seen != "" {
		t.Errorf("other tenant: status %d, tenant %q, want 404 and no handler", rec.Code, seen)
	}
}

func TestSecurity_A01_NonMemberAndFailuresAre404(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	cases := []struct {
		name string
		user string
		cfg  tenancy.Config
	}{
		{
			"no membership",
			"8",
			tenancy.Config{Resolve: tenancy.PathValue("ws"), Member: members(map[string]bool{"7/w1": true})},
		},
		{
			"anonymous",
			"",
			tenancy.Config{Resolve: tenancy.PathValue("ws"), Member: members(map[string]bool{"/w1": true})},
		},
		{"resolver error", "7", tenancy.Config{
			Resolve: func(*http.Request) (tenancy.ID, error) { return "w1", boom },
			Member:  members(map[string]bool{"7/w1": true}),
		}},
		{"empty tenant", "7", tenancy.Config{
			Resolve: func(*http.Request) (tenancy.ID, error) { return "", nil },
			Member:  func(context.Context, string, tenancy.ID) (bool, error) { return true, nil },
		}},
		{"member error", "7", tenancy.Config{
			Resolve: tenancy.PathValue("ws"),
			Member:  func(context.Context, string, tenancy.ID) (bool, error) { return true, boom },
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, seen := serve(t, tc.user, tc.cfg, "/w/w1")
			if rec.Code != http.StatusNotFound || seen != "" {
				t.Errorf("status %d, tenant %q, want 404 and no handler", rec.Code, seen)
			}
		})
	}
}

func TestMiddlewareRequiresConfig(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("Middleware with an empty Config did not panic")
		}
	}()

	tenancy.Middleware(tenancy.Config{})
}

type memSession map[string]any

func (s memSession) Get(k string) any    { return s[k] }
func (s memSession) Put(k string, v any) { s[k] = v }
func (memSession) Forget(...string)      {}
func (memSession) Pull(string) any       { return nil }
func (memSession) Flash(string, any)     {}
func (memSession) Regenerate()           {}
func (memSession) Invalidate()           {}
func (memSession) ID() string            { return "s" }

func TestSessionResolverAndSwitch(t *testing.T) {
	t.Parallel()

	sess := memSession{}
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	r = r.WithContext(web.WithSession(r.Context(), sess))

	if id, _ := tenancy.Session("ws")(r); id != "" {
		t.Errorf("fresh session resolved %q", id)
	}

	if err := tenancy.Switch(r, "ws", "w9"); err != nil {
		t.Fatal(err)
	}

	if id, _ := tenancy.Session("ws")(r); id != "w9" {
		t.Errorf("after Switch resolved %q, want w9", id)
	}

	if err := tenancy.Switch(r, "ws", ""); !errors.Is(err, tenancy.ErrNoTenant) {
		t.Errorf("Switch empty = %v", err)
	}

	bare := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	if _, err := tenancy.Session("ws")(bare); !errors.Is(err, tenancy.ErrNoSession) {
		t.Errorf("Session without middleware = %v", err)
	}

	if err := tenancy.Switch(bare, "ws", "w1"); !errors.Is(err, tenancy.ErrNoSession) {
		t.Errorf("Switch without middleware = %v", err)
	}
}

func TestHeaderResolver(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	r.Header.Set("X-Workspace", "w4")

	if id, _ := tenancy.Header("X-Workspace")(r); id != "w4" {
		t.Errorf("Header resolved %q", id)
	}
}

// FuzzMiddleware: whatever the client sends as tenant, the handler runs
// only for the one tenant the user belongs to.
func FuzzMiddleware(f *testing.F) {
	for _, s := range []string{"w1", "", "W1", "w1 ", "w1\x00", "../w1", "w1,w2"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, header string) {
		mw := tenancy.Middleware(tenancy.Config{
			Resolve: tenancy.Header("X-Workspace"),
			Member:  members(map[string]bool{"7/w1": true}),
		})

		var seen tenancy.ID

		h := asUser("7")(mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			seen, _ = tenancy.From(r.Context())
		})))

		r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		r.Header["X-Workspace"] = []string{header}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if (seen == "") != (rec.Code == http.StatusNotFound) || (seen != "" && seen != "w1") {
			t.Errorf("header %q: status %d, tenant %q", header, rec.Code, seen)
		}
	})
}
