package vukem

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func TestSecurity_A02_DevtoolsRefusedInProduction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		opts []Option
	}{
		{name: "option", env: map[string]string{"APP_ENV": "production"}, opts: []Option{WithDevtools()}},
		{name: "env", env: map[string]string{"APP_ENV": "production", "DEVTOOLS": "true"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(append(tt.opts, WithSource(testSource(tt.env)), WithLogger(discardLogger()))...)
			if err == nil || !strings.Contains(err.Error(), "devtools") {
				t.Fatalf("New() error = %v, want a devtools boot error", err)
			}
		})
	}

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()))
	if err != nil {
		t.Fatal(err)
	}

	if app.Devtools != nil {
		t.Fatal("devtools enabled by default")
	}

	if rec := devtoolsGet(t, app, "/_vukem/"); rec.Code != http.StatusNotFound {
		t.Errorf("GET /_vukem/ without devtools = %d, want 404", rec.Code)
	}
}

type welcomeMail struct{}

func (welcomeMail) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{"ada@example.com"}, Subject: "Welcome"}
}

func (welcomeMail) Content() mail.Content { return mail.HTML("<h1>Hi</h1>", "Hi") }

type userSignedUp struct{}

type devtoolsJob struct{}

func (devtoolsJob) Kind() string { return "devtools.ping" }

func TestDevtoolsRecordsAppRequest(t *testing.T) {
	t.Parallel()

	app, err := New(
		WithSource(testSource(map[string]string{
			"APP_ENV": "development", "DEVTOOLS": "true", "DATABASE_URL": ":memory:", "MAIL_MAILER": "array",
		})),
		WithLogger(discardLogger()),
		WithJobs(queue.Handle(func(context.Context, devtoolsJob) error { return nil })),
		WithRoutes(func(r *router.Router, app *App) error {
			if err := DefaultMiddleware(app, r); err != nil {
				return err
			}

			r.Get("/users", func(c *web.Ctx) error {
				ctx := c.Context()

				db, err := app.DB()
				if err != nil {
					return err
				}

				for i := range 4 {
					var n int
					if err := db.QueryRowContext(ctx, "SELECT ? + 1", i).Scan(&n); err != nil {
						return err
					}
				}

				app.Log.InfoContext(ctx, "listed users", "count", 4)

				if err := app.Events.Dispatch(ctx, userSignedUp{}); err != nil {
					return err
				}

				q, err := app.Queue()
				if err != nil {
					return err
				}

				if err := q.Dispatch(ctx, devtoolsJob{}); err != nil {
					return err
				}

				m, err := app.Mailer()
				if err != nil {
					return err
				}

				if err := m.Send(ctx, welcomeMail{}); err != nil {
					return err
				}

				return c.String(http.StatusOK, "ok")
			}).Name("users.index")

			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	if rec := devtoolsGet(t, app, "/users"); rec.Code != http.StatusOK {
		t.Fatalf("GET /users = %d: %s", rec.Code, rec.Body)
	}

	reqs := app.Devtools.Requests()
	if len(reqs) != 1 {
		t.Fatalf("Requests() = %d, want 1", len(reqs))
	}

	r := reqs[0]
	if r.Route != "users.index" || r.Queries != 4 || len(r.NPlusOne) != 1 || r.RequestID == "" {
		t.Errorf("request = %+v", r)
	}

	pages := map[string]string{
		"/_vukem/":                 "N+1 queries",
		"/_vukem/requests/" + r.ID: "listed users",
		"/_vukem/queries":          "SELECT ? &#43; ?",
		"/_vukem/jobs":             "ping",
		"/_vukem/events":           "vukem.userSignedUp",
		"/_vukem/mails":            "Welcome",
		"/_vukem/logs":             "count=4",
		"/_vukem/routes":           "users.index",
	}

	for path, want := range pages {
		rec := devtoolsGet(t, app, path)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("GET %s = %d, want 200 containing %q", path, rec.Code, want)
		}

		// The app's default CSP (SecureHeaders) applies and the page's
		// only <style> carries its nonce.
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "style-src 'self' 'nonce-") ||
			!strings.Contains(rec.Body.String(), `<style nonce="`) {
			t.Errorf("GET %s CSP = %q", path, csp)
		}
	}

	if got := len(app.Devtools.Requests()); got != 1 {
		t.Errorf("dashboard requests were recorded: %d requests", got)
	}
}

func devtoolsGet(t *testing.T, app *App, path string) *httptest.ResponseRecorder {
	t.Helper()

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	req.RemoteAddr, req.Host = "127.0.0.1:50000", "localhost:3000"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}
