package vukem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/logging/telegram"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/security"
	"github.com/AlexGalhardo/vukem/web"
)

type orderPlaced struct{ ID int }

func TestCacheFromConfig(t *testing.T) {
	t.Parallel()

	dbURL := "file:" + filepath.ToSlash(filepath.Join(testutil.TempDir(t), "c.db"))

	for store, wantErr := range map[string]bool{"memory": false, "database": false, "redis": true} {
		app, err := New(WithSource(testSource(map[string]string{"CACHE_STORE": store, "DATABASE_URL": dbURL})),
			WithLogger(discardLogger()))
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

		c, err := app.Cache()
		if (err != nil) != wantErr {
			t.Fatalf("CACHE_STORE=%s: %v", store, err)
		}

		if err != nil {
			continue
		}

		if err := c.Put(t.Context(), "k", 1, time.Minute); err != nil {
			t.Fatal(err)
		}

		if v, ok, _ := cache.Get[int](t.Context(), c, "k"); !ok || v != 1 {
			t.Fatalf("%s: get = %v %v", store, v, ok)
		}
	}
}

func TestEventsWiring(t *testing.T) {
	t.Parallel()

	var got []int

	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()),
		WithListeners(events.On(func(_ context.Context, e orderPlaced) error { got = append(got, e.ID); return nil })))
	if err != nil {
		t.Fatal(err)
	}

	if err := app.Events.Dispatch(t.Context(), orderPlaced{ID: 7}); err != nil || len(got) != 1 || got[0] != 7 {
		t.Fatalf("dispatch = %v %v", got, err)
	}

	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := app.Events.Dispatch(t.Context(), orderPlaced{}); !errors.Is(err, events.ErrClosed) {
		t.Fatalf("after shutdown = %v", err)
	}
}

func TestSecurityEventsReachTheBus(t *testing.T) {
	t.Parallel()

	var got []security.Event

	app := newTestApp(t, WithListeners(events.On(func(_ context.Context, e security.Event) error {
		got = append(got, e)
		return nil
	})), WithRoutes(func(r *router.Router, _ *App) error {
		r.Get("/deny", func(c *web.Ctx) error { return c.Authorize("anything", nil) })
		return nil
	}))

	if rec := doReq(t, app, "/deny"); rec.Code != 403 {
		t.Fatalf("status = %d", rec.Code)
	}

	if len(got) != 1 || got[0].Name != "authz.denied" || got[0].Attrs["ability"] != "anything" ||
		got[0].Path != "/deny" {
		t.Fatalf("events = %+v", got)
	}
}

// Database cache rows (rate limit counters among them) stay until pruned:
// the app schedules cache:prune hourly when the cache keeps expired rows.
func TestDatabaseCacheIsPrunedOnSchedule(t *testing.T) {
	t.Parallel()

	dbURL := "file:" + filepath.ToSlash(filepath.Join(testutil.TempDir(t), "c.db"))

	newApp := func(store string) *App {
		app, err := New(WithSource(testSource(map[string]string{"CACHE_STORE": store, "DATABASE_URL": dbURL})),
			WithLogger(discardLogger()))
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

		return app
	}

	scheduled := func(app *App) string {
		s, err := app.Schedule()
		if err != nil {
			t.Fatal(err)
		}

		for _, tk := range s.Tasks() {
			if tk.Name() == "cache:prune" {
				return tk.Expression()
			}
		}

		return ""
	}

	app := newApp("database")
	if got := scheduled(app); got != "0 * * * *" {
		t.Fatalf("cache:prune schedule = %q, want hourly", got)
	}

	c, err := app.Cache()
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	if err := c.Store().Set(ctx, "old", []byte("1"), time.Millisecond); err != nil {
		t.Fatal(err)
	}

	if err := c.Store().Set(ctx, "live", []byte("1"), time.Hour); err != nil {
		t.Fatal(err)
	}

	time.Sleep(5 * time.Millisecond) //nolint:forbidigo // real expiry in the database

	if err := app.Call(ctx, "cache:prune", nil); err != nil {
		t.Fatalf("cache:prune = %v", err)
	}

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cache").Scan(&n); err != nil || n != 1 {
		t.Fatalf("cache rows after prune = %d, %v, want 1", n, err)
	}

	if got := scheduled(newApp("memory")); got != "" {
		t.Fatalf("cache:prune scheduled for the memory cache (%q)", got)
	}
}

type hello struct{}

func (hello) Envelope() mail.Envelope { return mail.Envelope{To: []string{"a@b.co"}, Subject: "Hi"} }
func (hello) Content() mail.Content   { return mail.View("mail/hello", nil) }

func TestMailerFromConfig(t *testing.T) {
	t.Parallel()

	for env, want := range map[string]string{
		"array":    "*mail.Array",
		"log":      "mail.Log",
		"":         "mail.Log",
		"smtp":     "*mail.SMTP",
		"resend":   "error",
		"mailtrap": "error",
		"pigeon":   "error",
	} {
		app, err := New(WithSource(testSource(map[string]string{"MAIL_MAILER": env})), WithLogger(discardLogger()))
		if err != nil {
			t.Fatal(err)
		}

		m, err := app.Mailer()

		got := "error"
		if err == nil {
			got = strings.TrimPrefix(typeName(m.Transport()), "")
		}

		if got != want {
			t.Errorf("MAIL_MAILER=%q: transport %s (err %v), want %s", env, got, err, want)
		}
	}

	prod, _ := New(WithSource(testSource(map[string]string{"APP_ENV": "production"})), WithLogger(discardLogger()))
	if _, err := prod.Mailer(); err == nil {
		t.Fatal("production without MAIL_MAILER accepted")
	}

	withKey, _ := New(WithSource(testSource(map[string]string{"MAIL_MAILER": "resend", "RESEND_API_KEY": "re_x"})),
		WithLogger(discardLogger()))
	if m, err := withKey.Mailer(); err != nil || typeName(m.Transport()) != "*mail.Resend" {
		t.Fatalf("resend = %v", err)
	}
}

func TestMailerMailtrapFromConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"sending", map[string]string{"MAILTRAP_API_TOKEN": "mt_x"}, ""},
		{"sandbox", map[string]string{"MAILTRAP_API_TOKEN": "mt_x", "MAILTRAP_INBOX_ID": "4242"}, ""},
		{"missing token", map[string]string{"MAILTRAP_INBOX_ID": "4242"}, "MAILTRAP_API_TOKEN"},
		{
			"negative inbox",
			map[string]string{"MAILTRAP_API_TOKEN": "mt_x", "MAILTRAP_INBOX_ID": "-1"},
			"MAILTRAP_INBOX_ID",
		},
		{"production without token", map[string]string{"APP_ENV": "production"}, "MAILTRAP_API_TOKEN"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.env["MAIL_MAILER"] = "mailtrap"

			app, err := New(WithSource(testSource(tt.env)), WithLogger(discardLogger()))
			if err != nil {
				t.Fatal(err)
			}

			m, err := app.Mailer()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Mailer() = %v, want an error naming %s", err, tt.wantErr)
				}

				if strings.Contains(err.Error(), "mt_x") {
					t.Fatalf("Mailer() error leaks the token: %v", err)
				}

				return
			}

			if err != nil || typeName(m.Transport()) != "*mail.Mailtrap" {
				t.Fatalf("Mailer() = %v, %v, want *mail.Mailtrap", m, err)
			}
		})
	}
}

func TestMailerUsesAppViews(t *testing.T) {
	t.Parallel()

	arr := mail.NewArray()
	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithMailTransport(arr),
		WithViews(fstest.MapFS{"mail/hello.html": {Data: []byte("{{/* layout: none */}}<p>Hello</p>")}}))
	if err != nil {
		t.Fatal(err)
	}

	m, err := app.Mailer()
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Send(t.Context(), hello{}); err != nil {
		t.Fatal(err)
	}

	if sent := arr.Sent(); len(sent) != 1 || sent[0].HTML != "<p>Hello</p>" || sent[0].Text != "Hello" {
		t.Fatalf("sent = %+v", sent)
	}
}

func typeName(v any) string {
	return strings.ReplaceAll(fmtType(v), "github.com/AlexGalhardo/vukem/", "")
}

func fmtType(v any) string { return fmt.Sprintf("%T", v) }

func TestMailerMailtrapSandboxInProductionWarns(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		env  map[string]string
		warn bool
	}{
		{map[string]string{"APP_ENV": "production", "MAILTRAP_INBOX_ID": "4242"}, true},
		{map[string]string{"APP_ENV": "production"}, false},
		{map[string]string{"MAILTRAP_INBOX_ID": "4242"}, false},
	} {
		tt.env["MAIL_MAILER"] = "mailtrap"
		tt.env["MAILTRAP_API_TOKEN"] = "mt_x"

		var logs bytes.Buffer

		app, err := New(WithSource(testSource(tt.env)), WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
		if err != nil {
			t.Fatal(err)
		}

		if _, err := app.Mailer(); err != nil {
			t.Fatalf("Mailer(%v) = %v", tt.env, err)
		}

		if got := strings.Contains(logs.String(), "Mailtrap sandbox"); got != tt.warn {
			t.Errorf("Mailer(%v) warned = %v, want %v; logs:\n%s", tt.env, got, tt.warn, logs.String())
		}
	}
}

func TestTelegramFromConfig(t *testing.T) {
	t.Parallel()

	off, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()))
	if err != nil || off.Telegram != nil {
		t.Fatalf("without config: %v %v", off.Telegram, err)
	}

	var out bytes.Buffer

	on, err := New(WithSource(testSource(map[string]string{
		"TELEGRAM_BOT_TOKEN": "123:SUPERSECRET", "TELEGRAM_CHAT_ID": "-1", "TELEGRAM_MIN_LEVEL": "warn",
	})), WithLogger(discardLogger()), WithStdout(&out))
	if err != nil || on.Telegram == nil {
		t.Fatalf("with config: %v", err)
	}

	if !on.Telegram.Enabled(context.Background(), 4) || on.Telegram.Enabled(context.Background(), 0) {
		t.Fatal("MinLevel not applied")
	}

	cmd := aboutCommand()
	cmd.Flags(flag.NewFlagSet("about", flag.ContinueOnError))

	if err := cmd.Run(t.Context(), on, nil); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out.String(), "on (token [redacted])") || strings.Contains(out.String(), "SUPERSECRET") {
		t.Fatalf("about = %s", out.String())
	}

	if err := on.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := New(WithSource(testSource(map[string]string{
		"TELEGRAM_BOT_TOKEN": "x", "TELEGRAM_CHAT_ID": "-1", "TELEGRAM_MIN_LEVEL": "loud",
	})), WithLogger(discardLogger())); err == nil {
		t.Fatal("bad level accepted")
	}
}

func TestTelegramLogForwarding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		env       map[string]string
		configure func(*telegram.Config)
		wantLogs  int
	}{
		{"default forwards errors", nil, nil, 2},
		{"TELEGRAM_LOG=false keeps Send only", map[string]string{"TELEGRAM_LOG": "false"}, nil, 0},
		{"TELEGRAM_RATE_LIMIT caps records", map[string]string{"TELEGRAM_RATE_LIMIT": "1"}, nil, 1},
		{"WithTelegram filter", nil, func(c *telegram.Config) {
			c.Filter = func(_ context.Context, r slog.Record) bool { return r.Message == "second" }
		}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu   sync.Mutex
				sent []string
			)

			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Text string `json:"text"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)

				mu.Lock()
				sent = append(sent, body.Text)
				mu.Unlock()

				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			t.Cleanup(api.Close)

			env := map[string]string{"TELEGRAM_BOT_TOKEN": "123:T", "TELEGRAM_CHAT_ID": "-1"}
			maps.Copy(env, tt.env)

			app, err := New(WithSource(testSource(env)), WithLogger(discardLogger()),
				WithTelegram(func(c *telegram.Config) {
					c.BaseURL = api.URL
					if tt.configure != nil {
						tt.configure(c)
					}
				}))
			if err != nil {
				t.Fatal(err)
			}

			app.Log.Error("first")
			app.Log.Error("second")
			app.Telegram.Send(t.Context(), "explicit")

			if err := app.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}

			mu.Lock()
			defer mu.Unlock()

			if got := len(sent) - 1; got != tt.wantLogs || !slices.Contains(sent, "explicit") {
				t.Errorf("forwarded log records = %d, want %d (sent %q)", got, tt.wantLogs, sent)
			}
		})
	}
}

func TestTelegramAPIURLFromEnv(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		paths []string
	)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()

		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(api.Close)

	// A trailing slash is dropped, so the path is not //bot<token>.
	app, err := New(WithSource(testSource(map[string]string{
		"TELEGRAM_BOT_TOKEN": "123:T", "TELEGRAM_CHAT_ID": "-1", "TELEGRAM_LOG": "false",
		"TELEGRAM_API_URL": api.URL + "/",
	})), WithLogger(discardLogger()))
	if err != nil {
		t.Fatal(err)
	}

	if err := app.Telegram.SendSync(t.Context(), "hi"); err != nil {
		t.Fatalf("SendSync() = %v, want nil", err)
	}

	if err := app.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if diff := cmp.Diff([]string{"/bot123:T/sendMessage"}, paths); diff != "" {
		t.Errorf("Bot API requests (-want +got):\n%s", diff)
	}
}

// TELEGRAM_API_URL carries the bot token in its path, so it must be https
// outside development and test, and a bad value fails boot without echoing
// the URL or the token.
func TestSecurity_A04_TelegramAPIURLRequiresHTTPS(t *testing.T) {
	t.Parallel()

	const token = "123:SUPERSECRET"

	tests := []struct {
		name    string
		env     string
		url     string
		wantErr bool
	}{
		{"default", "production", "", false},
		{"https in production", "production", "https://bot-api.example.com", false},
		{"https with a path prefix", "production", "https://proxy.example.com/telegram", false},
		{"http in production", "production", "http://bot-api.example.com", true},
		{"http on loopback in production", "production", "http://127.0.0.1:8081", true},
		{"http in development", "development", "http://localhost:8081", false},
		{"http in test", "test", "http://127.0.0.1:8081", false},
		{"other scheme", "development", "ftp://bot-api.example.com", true},
		{"no host", "development", "https://", true},
		{"not a URL", "development", "bot-api.example.com", true},
		{"userinfo", "production", "https://user:" + token + "@bot-api.example.com", true},
		{"query", "production", "https://bot-api.example.com?token=" + token, true},
		{"fragment", "production", "https://bot-api.example.com#" + token, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := map[string]string{"APP_ENV": tt.env, "TELEGRAM_BOT_TOKEN": token, "TELEGRAM_CHAT_ID": "-1"}
			if tt.url != "" {
				env["TELEGRAM_API_URL"] = tt.url
			}

			app, err := New(WithSource(testSource(env)), WithLogger(discardLogger()))
			if (err != nil) != tt.wantErr {
				t.Fatalf(
					"New(TELEGRAM_API_URL=%q, APP_ENV=%s) error = %v, want error %v",
					tt.url,
					tt.env,
					err,
					tt.wantErr,
				)
			}

			if err != nil {
				if !strings.Contains(err.Error(), "TELEGRAM_API_URL") {
					t.Errorf("error %q does not name TELEGRAM_API_URL", err)
				}

				if strings.Contains(err.Error(), "SUPERSECRET") || strings.Contains(err.Error(), "example.com") {
					t.Errorf("error %q echoes the URL or the token", err)
				}

				return
			}

			if err := app.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTelegramStatusShowsLogForwarding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		log  string
		want string
	}{
		{"true", "on (token [redacted])"},
		{"false", "on (token [redacted], log forwarding off)"},
	}

	for _, tt := range tests {
		t.Run(tt.log, func(t *testing.T) {
			t.Parallel()

			app, err := New(WithSource(testSource(map[string]string{
				"TELEGRAM_BOT_TOKEN": "123:T", "TELEGRAM_CHAT_ID": "-1", "TELEGRAM_LOG": tt.log,
			})), WithLogger(discardLogger()))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

			if got := telegramStatus(app); got != tt.want {
				t.Errorf("telegramStatus(TELEGRAM_LOG=%s) = %q, want %q", tt.log, got, tt.want)
			}
		})
	}
}
