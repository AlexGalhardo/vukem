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
	"github.com/AlexGalhardo/vukem/flags"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/logging/telegram"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/schema"
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

type reactHello struct{ Name string }

func (reactHello) Envelope() mail.Envelope { return mail.Envelope{To: []string{"a@example.com"}} }
func (m reactHello) Content() mail.Content { return mail.React("hello", m) }

func TestMailerUsesReactEmails(t *testing.T) {
	t.Parallel()

	arr := mail.NewArray()
	app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithMailTransport(arr),
		WithViews(fstest.MapFS{"layouts/app.html": {Data: []byte(`<main>{{block "content" .}}{{end}}</main>`)}}),
		WithReactEmails(fstest.MapFS{"hello.html": {Data: []byte(`<!DOCTYPE html><html><p>Hi {{.Name}}</p></html>`)}}))
	if err != nil {
		t.Fatal(err)
	}

	m, err := app.Mailer()
	if err != nil {
		t.Fatal(err)
	}

	if err := m.Send(t.Context(), reactHello{Name: "<b>"}); err != nil {
		t.Fatal(err)
	}

	// No app layout around the exported document; the prop is escaped.
	if sent := arr.Sent(); len(sent) != 1 || sent[0].HTML != "<!DOCTYPE html><html><p>Hi &lt;b&gt;</p></html>" {
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
	if err != nil || off.Telegram == nil || off.Telegram.Configured() {
		t.Fatalf("without config: %v %v", off.Telegram, err)
	}

	// The unconfigured handler is safe to call and reports ErrDisabled.
	off.Telegram.Send(t.Context(), "dropped")

	if err := off.Telegram.SendSync(t.Context(), "x"); !errors.Is(err, telegram.ErrDisabled) {
		t.Fatalf("SendSync without config = %v, want ErrDisabled", err)
	}

	info, err := New(WithSource(testSource(map[string]string{
		"TELEGRAM_BOT_TOKEN": "123:x", "TELEGRAM_CHAT_ID": "-1", "TELEGRAM_MIN_LEVEL": "info",
	})), WithLogger(discardLogger()))
	if err != nil {
		t.Fatal(err)
	}

	if !info.Telegram.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("TELEGRAM_MIN_LEVEL=info does not forward info records")
	}

	if err := info.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
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

func TestFlagsFromConfig(t *testing.T) {
	t.Parallel()

	beta := flags.Bool("beta", false)
	locked := flags.Bool("admin_two_factor", true).Lock()

	for name, tc := range map[string]struct {
		env  map[string]string
		list []flags.AnyFlag
	}{
		"duplicate":              {nil, []flags.AnyFlag{beta, flags.Bool("beta", true)}},
		"bad value":              {map[string]string{"FLAG_BETA": "sometimes"}, []flags.AnyFlag{beta}},
		"locked override":        {map[string]string{"FLAG_ADMIN_TWO_FACTOR": "false"}, []flags.AnyFlag{locked}},
		"unknown flag key":       {map[string]string{"FLAG_BETTA": "true"}, []flags.AnyFlag{beta}},
		"flag key without flags": {map[string]string{"FLAG_BETA": "true"}, nil},
	} {
		if _, err := New(
			WithSource(testSource(tc.env)),
			WithLogger(discardLogger()),
			WithFlags(tc.list...),
		); err == nil {
			t.Errorf("%s: New() = nil error, want a boot error", name)
		}
	}

	// Without registered flags, Flags touches neither the database nor the cache.
	plain, err := New(WithSource(testSource(map[string]string{"DATABASE_URL": "postgres://unreachable"})),
		WithLogger(discardLogger()))
	if err != nil {
		t.Fatal(err)
	}

	if ff, err := plain.Flags(); err != nil || len(ff.All()) != 0 || plain.db != nil {
		t.Errorf("Flags() without flags = %v, %v (db opened: %v)", ff, err, plain.db != nil)
	}

	dbURL := "file:" + filepath.ToSlash(filepath.Join(testutil.TempDir(t), "f.db"))
	app, err := New(WithSource(testSource(map[string]string{"DATABASE_URL": dbURL, "FLAG_BETA": "true"})),
		WithLogger(discardLogger()), WithFlags(beta, locked),
		WithMigrations([]schema.Migration{flags.Migration("1_create_feature_flags_table")}))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	m, err := app.Migrator()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Up(t.Context()); err != nil {
		t.Fatal(err)
	}

	ff, err := app.Flags()
	if err != nil {
		t.Fatal(err)
	}

	if !ff.Enabled(t.Context(), beta) {
		t.Error("FLAG_BETA=true did not override the code default")
	}

	if _, err := ff.Set(t.Context(), flags.Global, "beta", "false"); err != nil {
		t.Fatal(err)
	}

	if again, _ := app.Flags(); again != ff || again.Enabled(t.Context(), beta) {
		t.Error("the stored value did not win over FLAG_BETA, or Flags() built a second instance")
	}
}

// A mail setting that can never send (a missing API token, a bad inbox id,
// an unknown mailer) stops Boot, not the first email in production.
func TestBootFailsOnBrokenMailConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		opts    []Option
		wantErr string
	}{
		{name: "resend without key", env: map[string]string{"MAIL_MAILER": "resend"}, wantErr: "RESEND_API_KEY"},
		{
			name:    "mailtrap without token",
			env:     map[string]string{"MAIL_MAILER": "mailtrap"},
			wantErr: "MAILTRAP_API_TOKEN",
		},
		{
			name: "negative inbox",
			env: map[string]string{
				"MAIL_MAILER":        "mailtrap",
				"MAILTRAP_API_TOKEN": "mt_x",
				"MAILTRAP_INBOX_ID":  "-1",
			},
			wantErr: "MAILTRAP_INBOX_ID",
		},
		{name: "unknown mailer", env: map[string]string{"MAIL_MAILER": "pigeon"}, wantErr: `"pigeon"`},
		{name: "resend with key", env: map[string]string{"MAIL_MAILER": "resend", "RESEND_API_KEY": "re_x"}},
		{name: "unset in development", env: map[string]string{}},
		{
			name: "custom transport",
			env:  map[string]string{"MAIL_MAILER": "resend"},
			opts: []Option{WithMailTransport(mail.NewArray())},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app, err := New(
				append([]Option{WithSource(testSource(tt.env)), WithLogger(discardLogger())}, tt.opts...)...)
			if err != nil {
				t.Fatal(err)
			}

			err = app.Boot(t.Context())
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Boot() = %v, want nil", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Boot() = %v, want an error naming %s", err, tt.wantErr)
			}
		})
	}
}

type signedUp struct{ Email string }

func TestWithEventsFakeRecordsInsteadOfRunning(t *testing.T) {
	t.Parallel()

	var ran bool

	f := events.NewFake()
	app := newTestApp(t,
		WithListeners(events.On(func(context.Context, signedUp) error { ran = true; return nil })),
		WithEventsFake(f),
	)

	if err := app.Events.Dispatch(t.Context(), signedUp{Email: "a@b.c"}); err != nil {
		t.Fatalf("Dispatch() = %v", err)
	}

	if ran {
		t.Error("listener ran under WithEventsFake, want it skipped")
	}

	if got := events.Dispatched[signedUp](f); len(got) != 1 || got[0].Email != "a@b.c" {
		t.Fatalf("Dispatched = %v, want one signedUp", got)
	}
}

// A81: the production mailer requirement stopped every command, not just
// the ones that send mail. install:auth registers routes that build a
// mailer, so resolving the routes failed, and `about` — a read-only
// command whose job is to report configuration — exited 1 with
// "set MAIL_MAILER in production". A generated image could not smoke-test
// itself. The requirement belongs where mail is sent, and at the boot of a
// process that will serve.
func TestProductionMailerIsRequiredOnlyWhereMailIsSent(t *testing.T) {
	t.Parallel()

	prod := map[string]string{"APP_ENV": "production", "APP_DEBUG": "false", "APP_URL": "https://example.com"}

	// A console command builds a mailer: no error, so route registration
	// and `about` work.
	console := newTestApp(t, WithSource(testSource(prod)), WithKind(KindConsole))

	m, err := console.Mailer()
	if err != nil {
		t.Fatalf("Mailer() in a console command = %v, want a mailer whose Send reports the problem", err)
	}

	// ... and sending is where it is refused, with the same message.
	err = m.Send(t.Context(), unconfiguredMailable{})
	if err == nil || !strings.Contains(err.Error(), "MAIL_MAILER") {
		t.Fatalf("Send() without a configured mailer = %v, want the MAIL_MAILER error", err)
	}

	// A serving process still refuses at boot: that is the deploy-time
	// signal, and it is the only place it protects a user.
	serving := newTestApp(t, WithSource(testSource(prod)))
	if _, err := serving.Mailer(); err == nil || !strings.Contains(err.Error(), "MAIL_MAILER") {
		t.Fatalf("Mailer() in a serving app = %v, want the MAIL_MAILER error", err)
	}

	// Outside production nothing changes: the log transport is the default.
	dev := newTestApp(t, WithKind(KindConsole))
	if _, err := dev.Mailer(); err != nil {
		t.Fatalf("Mailer() outside production = %v", err)
	}
}

type unconfiguredMailable struct{}

func (unconfiguredMailable) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{"a@example.com"}, Subject: "x"}
}

func (unconfiguredMailable) Content() mail.Content { return mail.Text("x") }

// A worker sends queued mail as its normal work, so it keeps the boot
// error that `about` lost: nothing is weakened, only moved off the
// commands that never send.
func TestRequireMailerRefusesAWorkerAndNotAReadOnlyCommand(t *testing.T) {
	t.Parallel()

	prod := map[string]string{"APP_ENV": "production", "APP_DEBUG": "false", "APP_URL": "https://example.com"}

	app := newTestApp(t, WithSource(testSource(prod)), WithKind(KindConsole))

	// Nothing has asked for a mailer yet: an app that never sends mail
	// must not be refused.
	if err := app.RequireMailer(); err != nil {
		t.Fatalf("RequireMailer() before any Mailer() call = %v, want nil", err)
	}

	if _, err := app.Mailer(); err != nil {
		t.Fatal(err)
	}

	if err := app.RequireMailer(); err == nil || !strings.Contains(err.Error(), "MAIL_MAILER") {
		t.Fatalf("RequireMailer() after Mailer() = %v, want the MAIL_MAILER error", err)
	}

	// A configured mailer is never refused.
	withLog := map[string]string{"MAIL_MAILER": "log"}
	maps.Copy(withLog, prod)

	ok := newTestApp(t, WithSource(testSource(withLog)), WithKind(KindConsole))
	if _, err := ok.Mailer(); err != nil {
		t.Fatal(err)
	}

	if err := ok.RequireMailer(); err != nil {
		t.Errorf("RequireMailer() with MAIL_MAILER=log = %v", err)
	}
}

func TestMailReplyToEnv(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithSource(testSource(map[string]string{
		"MAIL_MAILER":   "array",
		"MAIL_REPLY_TO": "support@example.com",
	})))

	m, err := app.Mailer()
	if err != nil {
		t.Fatal(err)
	}

	msg, err := m.Render(t.Context(), plainMail{})
	if err != nil || len(msg.ReplyTo) != 1 || msg.ReplyTo[0] != "<support@example.com>" {
		t.Fatalf("reply-to = %v %v", msg.ReplyTo, err)
	}
}
