package devtools

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func TestRingKeepsNewestWithinCapacity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		pushes int
		want   []int
	}{
		{name: "empty", pushes: 0, want: []int{}},
		{name: "partial", pushes: 2, want: []int{2, 1}},
		{name: "full", pushes: 3, want: []int{3, 2, 1}},
		{name: "wrapped", pushes: 7, want: []int{7, 6, 5}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRing[int](3)
			for i := range tt.pushes {
				r.push(i + 1)
			}

			if diff := cmp.Diff(tt.want, r.items()); diff != "" {
				t.Errorf("items() (-want +got):\n%s", diff)
			}

			if r.len() != len(tt.want) {
				t.Errorf("len() = %d, want %d", r.len(), len(tt.want))
			}
		})
	}
}

func TestCapacityBoundsEveryKind(t *testing.T) {
	t.Parallel()

	d := New(Config{Capacity: 4})
	ctx := t.Context()

	for range 10 {
		d.ObserveQuery(ctx, database.Query{SQL: "SELECT 1"})
		d.ObserveJob(ctx, jobEvent())
		d.ObserveEvent(ctx, eventOf())
		_ = d.LogHandler().Handle(ctx, slog.NewRecord(time.Now(), slog.LevelInfo, "x", 0))
		_ = d.MailTransport(mail.NewArray()).Send(ctx, &mail.Message{Subject: "s"})
	}

	got := []int{len(d.Queries()), len(d.Jobs()), len(d.Events()), len(d.Logs()), len(d.Mails())}
	if diff := cmp.Diff([]int{4, 4, 4, 4, 4}, got); diff != "" {
		t.Errorf("lengths (-want +got):\n%s", diff)
	}
}

func TestNormalizeSQL(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"SELECT * FROM users WHERE id = ?", "SELECT * FROM users WHERE id = ?"},
		{"SELECT *\n\tFROM users   WHERE id = $1", "SELECT * FROM users WHERE id = ?"},
		{"SELECT * FROM t1 WHERE name = 'O''Brien' AND age > 42", "SELECT * FROM t1 WHERE name = ? AND age > ?"},
		{"SELECT * FROM posts WHERE id IN ($1, $2, $3)", "SELECT * FROM posts WHERE id IN (?...)"},
		{"INSERT INTO t (a, b) VALUES (?, ?)", "INSERT INTO t (a, b) VALUES (?...)"},
	}

	for _, tt := range tests {
		if got := NormalizeSQL(tt.in); got != tt.want {
			t.Errorf("NormalizeSQL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSecurity_A09_DevtoolsRedactsQueryArgs(t *testing.T) {
	t.Parallel()

	secret := "hunter2-password"
	args := []any{
		secret, []byte("token-bytes"), 42, 3.5, true, nil, time.Now(), sql.Named("email", "a@b.c"),
		struct{ X string }{"x"},
	}

	got := RedactArgs(args)
	want := []string{
		"string(len=16)", "bytes(len=11)", "int", "float", "bool", "NULL", "time", "email=string(len=5)",
		"struct { X string }",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("RedactArgs (-want +got):\n%s", diff)
	}

	d := New(Config{})
	d.ObserveQuery(
		t.Context(),
		database.Query{SQL: "SELECT * FROM users WHERE password = '" + secret + "' AND x = ?", Args: args},
	)

	h := handler(t, d)
	for _, path := range []string{"/_vukem/queries", "/_vukem/"} {
		if body := get(t, h, path, "127.0.0.1:5000", "localhost:3000").Body.String(); strings.Contains(body, secret) ||
			strings.Contains(body, "a@b.c") {
			t.Errorf("GET %s leaks a bound or literal value", path)
		}
	}
}

func TestNPlusOneDetection(t *testing.T) {
	t.Parallel()

	d := New(Config{NPlusOneThreshold: 3})
	r := router.New(nil)
	r.UseServer(d.Middleware())
	r.Use(d.RouteMiddleware())

	r.Get("/posts", func(c *web.Ctx) error {
		ctx := c.Context()
		d.ObserveQuery(ctx, database.Query{SQL: "SELECT * FROM posts"})

		for i := range 4 {
			d.ObserveQuery(ctx, database.Query{SQL: "SELECT * FROM users WHERE id = $1", Args: []any{i}})
		}

		for range 3 {
			d.ObserveQuery(ctx, database.Query{SQL: "UPDATE counters SET n = n + 1"})
		}

		return c.String(http.StatusOK, "ok")
	})
	r.Get("/once", func(c *web.Ctx) error {
		d.ObserveQuery(c.Context(), database.Query{SQL: "SELECT * FROM users WHERE id = $1"})
		return c.NoContent()
	})
	d.Register(r)

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	get(t, h, "/posts", "127.0.0.1:1", "localhost")
	get(t, h, "/once", "127.0.0.1:1", "localhost")

	reqs := d.Requests()
	if len(reqs) != 2 {
		t.Fatalf("Requests() = %d, want 2", len(reqs))
	}

	if len(reqs[0].NPlusOne) != 0 {
		t.Errorf("single query flagged: %+v", reqs[0].NPlusOne)
	}

	want := []Repeat{{SQL: "SELECT * FROM users WHERE id = ?", Count: 4}}
	if diff := cmp.Diff(want, reqs[1].NPlusOne); diff != "" {
		t.Errorf("NPlusOne (-want +got):\n%s", diff)
	}

	if reqs[1].Queries != 8 || reqs[1].Pattern != "GET /posts" {
		t.Errorf("request = %+v", reqs[1])
	}

	var flagged int

	for _, q := range d.Queries() {
		if q.NPlusOne {
			flagged++
		}
	}

	// The 3rd and 4th repetition are flagged; the UPDATE repeats are not.
	if flagged != 2 {
		t.Errorf("flagged queries = %d, want 2", flagged)
	}

	body := get(t, h, "/_vukem/queries", "127.0.0.1:1", "localhost").Body.String()
	if !strings.Contains(body, "N+1") {
		t.Error("queries page has no N+1 badge")
	}
}

func TestSecurity_A01_DevtoolsLoopbackOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		remote string
		host   string
		header string
		allow  bool
		want   int
	}{
		{name: "ipv4 loopback", remote: "127.0.0.1:5000", host: "localhost:3000", want: 200},
		{name: "ipv6 loopback", remote: "[::1]:5000", host: "[::1]:3000", want: 200},
		{name: "loopback ip host", remote: "127.0.0.1:5000", host: "127.0.0.1:3000", want: 200},
		{name: "dot localhost", remote: "127.0.0.1:5000", host: "shop.localhost:3000", want: 200},
		{name: "remote client", remote: "192.168.1.20:5000", host: "localhost:3000", want: 404},
		{name: "public client", remote: "203.0.113.9:5000", host: "localhost:3000", want: 404},
		{name: "dns rebinding host", remote: "127.0.0.1:5000", host: "evil.example:3000", want: 404},
		{name: "through a proxy", remote: "127.0.0.1:5000", host: "localhost", header: "X-Forwarded-For", want: 404},
		{name: "forwarded", remote: "127.0.0.1:5000", host: "localhost", header: "Forwarded", want: 404},
		{
			name:   "tunnel setting only the scheme",
			remote: "127.0.0.1:5000",
			host:   "localhost",
			header: "X-Forwarded-Proto",
			want:   404,
		},
		{name: "forwarded port", remote: "127.0.0.1:5000", host: "localhost", header: "X-Forwarded-Port", want: 404},
		{name: "via a proxy", remote: "127.0.0.1:5000", host: "localhost", header: "Via", want: 404},
		{name: "garbage remote", remote: "nonsense", host: "localhost", want: 404},
		{name: "insecure allow remote", remote: "203.0.113.9:5000", host: "app.example", allow: true, want: 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := handler(t, New(Config{InsecureAllowRemote: tt.allow}))

			for _, path := range []string{"/_vukem/", "/_vukem/requests", "/_vukem/logs"} {
				req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
				req.RemoteAddr, req.Host = tt.remote, tt.host

				if tt.header != "" {
					req.Header.Set(tt.header, "203.0.113.9")
				}

				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)

				if rec.Code != tt.want {
					t.Errorf("GET %s = %d, want %d", path, rec.Code, tt.want)
				}

				if tt.want == 404 && strings.Contains(rec.Body.String(), "devtools") {
					t.Errorf("GET %s reveals the dashboard", path)
				}
			}
		})
	}
}

func TestSecurity_A05_DevtoolsMailPreviewIsSandboxed(t *testing.T) {
	t.Parallel()

	d := New(Config{})
	evil := `<p style="color:red">Hi</p><script>alert(document.cookie)</script><img src=x onerror="alert(1)">"`

	err := d.MailTransport(mail.NewArray()).Send(t.Context(), &mail.Message{
		Subject: "Welcome <b>", To: []string{"a@example.com"}, HTML: evil, Text: "Hi <script>",
	})
	if err != nil {
		t.Fatal(err)
	}

	h := handler(t, d)
	rec := get(t, h, "/_vukem/mails/"+d.Mails()[0].ID, "127.0.0.1:1", "localhost")
	body := rec.Body.String()

	if strings.Contains(body, "<script>") || strings.Contains(body, "<img src=x") || strings.Contains(body, "<b>") {
		t.Fatalf("mail HTML rendered inline:\n%s", body)
	}

	if !strings.Contains(
		body,
		`<iframe sandbox="" referrerpolicy="no-referrer" title="Mail preview" srcdoc="&lt;p style=&#34;color:red&#34;&gt;`,
	) {
		t.Errorf("mail HTML is not in an escaped, sandboxed srcdoc:\n%s", body)
	}

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script-src") {
		t.Errorf("mail CSP = %q, want no script source", csp)
	}

	list := get(t, h, "/_vukem/mails", "127.0.0.1:1", "localhost").Body.String()
	if strings.Contains(list, "<script>") || !strings.Contains(list, "Welcome &lt;b&gt;") {
		t.Errorf("mail list does not escape the subject")
	}
}

func TestLogHandlerKeepsAttrsAndGroups(t *testing.T) {
	t.Parallel()

	d := New(Config{Level: slog.LevelInfo})
	log := slog.New(d.LogHandler()).With("app", "shop").WithGroup("req")

	log.Debug("hidden")
	log.Info("served", "status", 200, slog.Group("user", "id", 7))
	log.Error("failed", "err", errors.New("boom"))

	got := d.Logs()
	if len(got) != 2 {
		t.Fatalf("Logs() = %d, want 2", len(got))
	}

	if got[1].Message != "served" || got[1].Attrs != "app=shop req.status=200 req.user.id=7" {
		t.Errorf("record = %+v", got[1])
	}

	if got[0].Level != "ERROR" || got[0].Attrs != "app=shop req.err=boom" {
		t.Errorf("record = %+v", got[0])
	}
}

func TestTraceTimelineAndPages(t *testing.T) {
	t.Parallel()

	d := New(Config{})
	r := router.New(nil)
	r.UseServer(d.Middleware())
	r.Use(d.RouteMiddleware())
	d.cfg.Routes = r.Routes

	log := slog.New(d.LogHandler())
	r.Get("/users/{id}", func(c *web.Ctx) error {
		ctx := c.Context()
		d.ObserveQuery(ctx, database.Query{SQL: "SELECT * FROM users WHERE id = ?", Args: []any{1}})
		log.InfoContext(ctx, "loaded user")
		d.ObserveEvent(ctx, eventOf())
		d.ObserveJob(ctx, jobEvent())
		_ = d.MailTransport(mail.NewArray()).Send(ctx, &mail.Message{Subject: "Hello", To: []string{"a@b.c"}})

		return c.String(http.StatusCreated, "ok")
	}).Name("users.show")
	d.Register(r)

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	get(t, h, "/users/1", "127.0.0.1:1", "localhost")

	// Dashboard requests are never recorded.
	get(t, h, "/_vukem/requests", "127.0.0.1:1", "localhost")

	reqs := d.Requests()
	if len(reqs) != 1 || reqs[0].Route != "users.show" || reqs[0].Status != http.StatusCreated {
		t.Fatalf("Requests() = %+v", reqs)
	}

	tr, ok := d.Trace(reqs[0].ID)
	if !ok {
		t.Fatal("Trace() not found")
	}

	var kinds []string
	for _, it := range tr.Items {
		kinds = append(kinds, it.Kind)
	}

	if diff := cmp.Diff([]string{"event", "job", "log", "mail", "query"}, sortedCopy(kinds)); diff != "" {
		t.Errorf("timeline kinds (-want +got):\n%s", diff)
	}

	pages := map[string]string{
		"/_vukem/":                          "Recent requests",
		"/_vukem/requests":                  "/users/1",
		"/_vukem/requests/" + tr.Request.ID: "loaded user",
		"/_vukem/queries":                   "SELECT * FROM users WHERE id = ?",
		"/_vukem/jobs":                      "send_invoice",
		"/_vukem/events":                    "devtools.orderPlaced",
		"/_vukem/mails":                     "Hello",
		"/_vukem/logs":                      "loaded user",
		"/_vukem/routes":                    "users.show",
	}

	for path, want := range pages {
		rec := get(t, h, path, "127.0.0.1:1", "localhost")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("GET %s = %d, want 200 containing %q", path, rec.Code, want)
		}

		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "'nonce-") {
			t.Errorf("GET %s CSP = %q", path, rec.Header().Get("Content-Security-Policy"))
		}
	}

	if rec := get(t, h, "/_vukem/requests/nope", "127.0.0.1:1", "localhost"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown trace = %d, want 404", rec.Code)
	}

	if body := get(
		t,
		h,
		"/_vukem/routes",
		"127.0.0.1:1",
		"localhost",
	).Body.String(); strings.Contains(
		body,
		"<code>/_vukem/logs</code>",
	) {
		t.Error("routes page lists the dashboard routes")
	}

	if body := get(
		t,
		h,
		"/_vukem/?refresh=1",
		"127.0.0.1:1",
		"localhost",
	).Body.String(); !strings.Contains(
		body,
		`content="2"`,
	) {
		t.Error("refresh is not clamped to 2s")
	}
}

func eventOf() events.Event {
	return events.Event{Type: "devtools.orderPlaced", Listeners: 1}
}

func jobEvent() queue.JobEvent {
	return queue.JobEvent{ID: "j1", Kind: "send_invoice", Queue: "default", Status: queue.JobDispatched}
}

func handler(t *testing.T, d *Devtools) http.Handler {
	t.Helper()

	r := router.New(nil)
	r.UseServer(d.Middleware())
	d.Register(r)

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	return h
}

func get(t *testing.T, h http.Handler, path, remote, host string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	req.RemoteAddr, req.Host = remote, host

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	slices.Sort(out)

	return out
}
