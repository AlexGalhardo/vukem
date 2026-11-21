package telegram_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/contrib/telegram"
)

const token = "123456:SECRET-TOKEN"

// fakeAPI is a stand-in for the Bot API; it never talks to the network.
type fakeAPI struct {
	srv *httptest.Server

	mu       sync.Mutex
	calls    []string
	bodies   []map[string]any
	handlers map[string]func(w http.ResponseWriter, body map[string]any)
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()

	f := &fakeAPI{handlers: map[string]func(http.ResponseWriter, map[string]any){}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/bot"+token+"/") {
			http.Error(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, http.StatusUnauthorized)

			return
		}

		method := strings.TrimPrefix(r.URL.Path, "/bot"+token+"/")

		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)

		f.mu.Lock()
		f.calls = append(f.calls, method)
		f.bodies = append(f.bodies, body)
		h := f.handlers[method]
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		if h == nil {
			if method == "sendMessage" {
				_, _ = w.Write(
					[]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":1,"type":"private"},"date":1}}`),
				)

				return
			}

			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))

			return
		}

		h(w, body)
	}))

	t.Cleanup(f.srv.Close)

	return f
}

func (f *fakeAPI) on(method string, h func(http.ResponseWriter, map[string]any)) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.handlers[method] = h
}

func (f *fakeAPI) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0

	for _, c := range f.calls {
		if c == method {
			n++
		}
	}

	return n
}

type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.String()
}

func newBot(t *testing.T, f *fakeAPI, mut func(*telegram.Config)) (*telegram.Bot, *logBuf) {
	t.Helper()

	logs := &logBuf{}
	cfg := telegram.Config{
		Token: config.NewSecret(token), BaseURL: f.srv.URL, Logger: slog.New(slog.NewTextHandler(logs, nil)),
		Sleep: func(context.Context, time.Duration) error { return nil },
	}

	if mut != nil {
		mut(&cfg)
	}

	bot, err := telegram.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	return bot, logs
}

func msg(id int64, text string) telegram.Update {
	return telegram.Update{ID: id, Message: &telegram.Message{
		ID: id, From: &telegram.User{ID: 7, FirstName: "Ana"}, Chat: telegram.Chat{ID: 99, Type: "private"}, Text: text,
	}}
}

func TestSendMessageAndErrors(t *testing.T) {
	t.Parallel()

	f := newFakeAPI(t)
	f.on("sendMessage", func(w http.ResponseWriter, body map[string]any) {
		if body["text"] == "boom" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(
				w,
				`{"ok":false,"error_code":400,"description":"Bad Request: chat not found for %s"}`,
				token,
			)

			return
		}

		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":5,"chat":{"id":99,"type":"private"},"date":1}}`))
	})

	bot, _ := newBot(t, f, nil)

	m, err := bot.SendMessage(t.Context(), telegram.SendMessage{ChatID: 99, Text: "hi"})
	if err != nil || m.ID != 5 {
		t.Fatalf("SendMessage = %+v, %v", m, err)
	}

	_, err = bot.SendMessage(t.Context(), telegram.SendMessage{ChatID: 99, Text: "boom"})

	api, ok := errors.AsType[*telegram.APIError](err)
	if !ok || api.Code != 400 || strings.Contains(err.Error(), token) {
		t.Errorf("error = %v, want an APIError 400 with the token scrubbed", err)
	}
}

func TestRetryAfterOn429(t *testing.T) {
	t.Parallel()

	f := newFakeAPI(t)

	var slept []time.Duration

	hits := 0

	f.on("sendMessage", func(w http.ResponseWriter, _ map[string]any) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write(
				[]byte(
					`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 4","parameters":{"retry_after":4}}`,
				),
			)

			return
		}

		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":1,"type":"private"},"date":1}}`))
	})

	bot, _ := newBot(t, f, func(c *telegram.Config) {
		c.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	})

	if _, err := bot.SendMessage(t.Context(), telegram.SendMessage{ChatID: 1, Text: "x"}); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]time.Duration{4 * time.Second, 4 * time.Second}, slept); diff != "" {
		t.Errorf("waits (-want +got):\n%s", diff)
	}

	// Past the retries, and past MaxRetryAfter, the 429 is the error.
	hits = -100

	bot, _ = newBot(t, f, func(c *telegram.Config) { c.Retries = 1 })

	_, err := bot.SendMessage(t.Context(), telegram.SendMessage{ChatID: 1, Text: "x"})
	if api, ok := errors.AsType[*telegram.APIError](err); !ok || api.Code != 429 || api.RetryAfter != 4*time.Second {
		t.Errorf("exhausted retries = %v, want a 429 with retry_after", err)
	}

	f.on("sendMessage", func(w http.ResponseWriter, _ map[string]any) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"x","parameters":{"retry_after":3600}}`))
	})

	before := f.count("sendMessage")

	if _, err := bot.SendMessage(
		t.Context(),
		telegram.SendMessage{ChatID: 1, Text: "x"},
	); err == nil ||
		f.count("sendMessage")-before != 1 {
		t.Errorf("a retry_after of an hour must be returned, not waited for (calls %d)", f.count("sendMessage")-before)
	}
}

func TestSecurity_A09_TokenNeverInErrorsOrLogs(t *testing.T) {
	t.Parallel()

	f := newFakeAPI(t)
	bot, logs := newBot(t, f, nil)

	f.srv.Close() // the transport error now carries the URL, which holds the token

	_, err := bot.SendMessage(t.Context(), telegram.SendMessage{ChatID: 1, Text: "x"})
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("transport error = %v, want it without the token", err)
	}

	r := telegram.NewRouter()
	r.Command("boom", func(context.Context, *telegram.Context) error { return errors.New("failed with " + token) })

	h, _ := bot.WebhookHandler(r, config.NewSecret("hook"), telegram.WebhookOptions{})
	rec := httptest.NewRecorder()

	req := httptest.NewRequest(
		http.MethodPost,
		"/",
		strings.NewReader(`{"update_id":1,"message":{"message_id":1,"chat":{"id":1,"type":"private"},"text":"/boom"}}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(telegram.SecretHeader, "hook")
	h.ServeHTTP(rec, req)

	if strings.Contains(logs.String(), "SECRET") && !strings.Contains(logs.String(), "[redacted]") {
		t.Errorf("the log carries the token: %s", logs.String())
	}

	if s := fmt.Sprintf(
		"%v %+v %#v",
		config.NewSecret(token),
		config.NewSecret(token),
		config.NewSecret(token),
	); strings.Contains(
		s,
		"SECRET",
	) {
		t.Errorf("config.Secret printed %s", s)
	}
}

func TestRouter(t *testing.T) {
	t.Parallel()

	f := newFakeAPI(t)
	bot, _ := newBot(t, f, nil)

	var got []string

	rec := func(name string) telegram.Handler {
		return func(_ context.Context, c *telegram.Context) error {
			got = append(got, name+":"+strings.Join(c.Args, ","))

			return nil
		}
	}

	r := telegram.NewRouter()
	r.Username = "MyBot"

	var order []string

	r.Use(func(next telegram.Handler) telegram.Handler {
		return func(ctx context.Context, c *telegram.Context) error {
			order = append(order, "a")

			return next(ctx, c)
		}
	}, func(next telegram.Handler) telegram.Handler {
		return func(ctx context.Context, c *telegram.Context) error {
			order = append(order, "b")

			return next(ctx, c)
		}
	})
	r.Command("start", rec("start"))
	r.Command("pay", rec("pay"))
	r.Callback("buy:", rec("buy"))
	r.Message(rec("message"))
	r.Fallback(rec("fallback"))

	cb := telegram.Update{
		ID:            9,
		CallbackQuery: &telegram.CallbackQuery{ID: "c1", From: telegram.User{ID: 7}, Data: "buy:42"},
	}

	for _, u := range []telegram.Update{
		msg(1, "/start"), msg(2, "/pay 10 usd"), msg(3, "/START@mybot"), msg(4, "/start@otherbot"), msg(5, "hello"),
		msg(6, "/unknown"), cb,
		{ID: 10},
	} {
		if err := r.HandleUpdate(t.Context(), bot, u); err != nil {
			t.Fatalf("HandleUpdate(%d) = %v", u.ID, err)
		}
	}

	want := []string{"start:", "pay:10,usd", "start:", "fallback:", "message:", "fallback:", "buy:", "fallback:"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("handlers (-want +got):\n%s", diff)
	}

	if len(order) < 2 || order[0] != "a" || order[1] != "b" {
		t.Errorf("middleware order = %v, want the first added to run first", order)
	}

	empty := telegram.NewRouter()
	if err := empty.HandleUpdate(t.Context(), bot, msg(1, "x")); !errors.Is(err, telegram.ErrNoHandler) {
		t.Errorf("no route = %v, want ErrNoHandler", err)
	}

	// A panic is an error, not a crash.
	r2 := telegram.NewRouter()
	r2.Command("p", func(context.Context, *telegram.Context) error { panic("kaboom") })

	if err := r2.HandleUpdate(t.Context(), bot, msg(1, "/p")); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Errorf("panic = %v, want an error", err)
	}
}

func TestConversationState(t *testing.T) {
	t.Parallel()

	f := newFakeAPI(t)
	bot, _ := newBot(t, f, nil)

	now := time.Unix(1_000, 0)
	store := telegram.NewMemoryStore()
	store.Now = func() time.Time { return now }
	store.TTL = time.Hour

	var amounts []string

	r := telegram.NewRouter()
	r.Use(telegram.Conversations(store, nil))
	r.Command("pay", func(ctx context.Context, c *telegram.Context) error {
		c.SetState(telegram.State{Step: "amount"})

		return c.Reply(ctx, "How much?")
	})
	r.Step("amount", func(ctx context.Context, c *telegram.Context) error {
		amounts = append(amounts, c.Update.Message.Text)
		c.ClearState()

		return c.Reply(ctx, "ok")
	})
	r.Message(func(ctx context.Context, c *telegram.Context) error { return c.Reply(ctx, "what?") })

	steps := []struct {
		text string
		wait time.Duration
	}{{"/pay", 0}, {"10", 0}, {"20", 0}, {"/pay", 0}, {"30", 2 * time.Hour}}

	for i, s := range steps {
		now = now.Add(s.wait)

		if err := r.HandleUpdate(t.Context(), bot, msg(int64(i), s.text)); err != nil {
			t.Fatal(err)
		}
	}

	// 10 answers the step; 20 finds no state; 30 comes after the state expired.
	if diff := cmp.Diff([]string{"10"}, amounts); diff != "" {
		t.Errorf("amounts (-want +got):\n%s", diff)
	}

	// Two users in one chat keep separate conversations.
	a, b := msg(1, "/pay"), msg(2, "99")
	b.Message.From = &telegram.User{ID: 8}

	_ = r.HandleUpdate(t.Context(), bot, a)
	_ = r.HandleUpdate(t.Context(), bot, b)

	if len(amounts) != 1 {
		t.Errorf("another user finished the conversation: %v", amounts)
	}
}

func hook(t *testing.T, h telegram.UpdateHandler, opts telegram.WebhookOptions) (http.Handler, *logBuf) {
	t.Helper()

	bot, logs := newBot(t, newFakeAPI(t), nil)

	wh, err := bot.WebhookHandler(h, config.NewSecret("s3cr3t-Token_1"), opts)
	if err != nil {
		t.Fatal(err)
	}

	return wh, logs
}

type handlerFunc func(context.Context, *telegram.Bot, telegram.Update) error

func (f handlerFunc) HandleUpdate(ctx context.Context, b *telegram.Bot, u telegram.Update) error {
	return f(ctx, b, u)
}

func TestSecurity_A07_WebhookSecretAndLimits(t *testing.T) {
	t.Parallel()

	var handled int

	wh, _ := hook(t, handlerFunc(func(context.Context, *telegram.Bot, telegram.Update) error {
		handled++

		return nil
	}), telegram.WebhookOptions{MaxBodyBytes: 512})

	update := `{"update_id":1,"message":{"message_id":1,"chat":{"id":1,"type":"private"},"text":"hi"}}`

	tests := []struct {
		name   string
		method string
		secret string
		ctype  string
		body   string
		want   int
	}{
		{"valid", "POST", "s3cr3t-Token_1", "application/json", update, 200},
		{"valid with charset", "POST", "s3cr3t-Token_1", "application/json; charset=utf-8", update, 200},
		{"no secret", "POST", "", "application/json", update, 401},
		{"wrong secret", "POST", "s3cr3t-Token_2", "application/json", update, 401},
		{"prefix of the secret", "POST", "s3cr3t", "application/json", update, 401},
		{"secret with a suffix", "POST", "s3cr3t-Token_1x", "application/json", update, 401},
		{"GET", "GET", "s3cr3t-Token_1", "application/json", "", 405},
		{"wrong content type", "POST", "s3cr3t-Token_1", "text/plain", update, 415},
		{"not json", "POST", "s3cr3t-Token_1", "application/json", "{nope", 400},
		{
			"oversized",
			"POST",
			"s3cr3t-Token_1",
			"application/json",
			`{"update_id":1,"pad":"` + strings.Repeat("a", 2000) + `"}`,
			413,
		},
	}

	for _, tc := range tests {
		before := handled

		r := httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.ctype)

		if tc.secret != "" {
			r.Header.Set(telegram.SecretHeader, tc.secret)
		}

		rec := httptest.NewRecorder()
		wh.ServeHTTP(rec, r)

		if rec.Code != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, rec.Code, tc.want)
		}

		if ran := handled - before; (tc.want == 200) != (ran == 1) {
			t.Errorf("%s: handler ran %d times for status %d", tc.name, ran, rec.Code)
		}
	}
}

func TestWebhookHandlerFailuresAre200(t *testing.T) {
	t.Parallel()

	wh, logs := hook(t, handlerFunc(func(context.Context, *telegram.Bot, telegram.Update) error {
		return errors.New("database unavailable")
	}), telegram.WebhookOptions{})

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"update_id":4}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(telegram.SecretHeader, "s3cr3t-Token_1")

	rec := httptest.NewRecorder()
	wh.ServeHTTP(rec, r)

	if rec.Code != 200 || !strings.Contains(logs.String(), "database unavailable") ||
		strings.Contains(rec.Body.String(), "database") {
		t.Errorf("handler failure = %d, log %q, want a 200 with the cause logged only", rec.Code, logs.String())
	}
}

func TestSecurity_A05_WebhookRefusesWeakSecrets(t *testing.T) {
	t.Parallel()

	bot, _ := newBot(t, newFakeAPI(t), nil)

	for name, s := range map[string]string{"empty": "", "space": "a b", "slash": "a/b", "long": strings.Repeat("a", 257), "unicode": "sécret"} {
		if _, err := bot.WebhookHandler(
			telegram.NewRouter(),
			config.NewSecret(s),
			telegram.WebhookOptions{},
		); err == nil {
			t.Errorf("%s secret was accepted", name)
		}
	}

	if _, err := telegram.New(telegram.Config{}); err == nil {
		t.Error("New without a token succeeded")
	}
}

func TestPoll(t *testing.T) {
	t.Parallel()

	f := newFakeAPI(t)

	var offsets []float64

	polls := 0

	f.on("getUpdates", func(w http.ResponseWriter, body map[string]any) {
		offsets = append(offsets, body["offset"].(float64))
		polls++

		switch polls {
		case 1:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":500,"description":"oops"}`))
		case 2:
			_, _ = w.Write(
				[]byte(
					`{"ok":true,"result":[{"update_id":10,"message":{"message_id":1,"chat":{"id":1,"type":"private"},"text":"a"}},{"update_id":11,"message":{"message_id":2,"chat":{"id":1,"type":"private"},"text":"b"}}]}`,
				),
			)
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		}
	})

	var (
		mu   sync.Mutex
		seen []string
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var slept int

	bot, _ := newBot(t, f, func(c *telegram.Config) {
		c.Sleep = func(context.Context, time.Duration) error { slept++; return nil }
	})

	done := make(chan error, 1)

	go func() {
		done <- bot.Poll(ctx, handlerFunc(func(_ context.Context, _ *telegram.Bot, u telegram.Update) error {
			mu.Lock()
			seen = append(seen, u.Message.Text)
			n := len(seen)
			mu.Unlock()

			if n == 2 {
				cancel()
			}

			return nil
		}), telegram.PollOptions{})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Poll = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Poll did not stop")
	}

	if diff := cmp.Diff([]string{"a", "b"}, seen); diff != "" {
		t.Errorf("updates (-want +got):\n%s", diff)
	}

	if len(offsets) < 2 || offsets[0] != 0 || offsets[1] != 0 || slept != 1 {
		t.Errorf("offsets %v, backoffs %d: want a retry after the failure with offset 0", offsets, slept)
	}

	// A rejected token ends the loop.
	bad, _ := newBot(t, f, func(c *telegram.Config) { c.Token = config.NewSecret("999:WRONG") })

	if err := bad.Poll(
		t.Context(),
		telegram.NewRouter(),
		telegram.PollOptions{},
	); err == nil ||
		errors.Is(err, context.Canceled) {
		t.Errorf("Poll with a bad token = %v, want the 401", err)
	}
}

func TestCallbackAnswer(t *testing.T) {
	t.Parallel()

	f := newFakeAPI(t)
	bot, _ := newBot(t, f, nil)

	r := telegram.NewRouter()
	r.Callback("ok", func(ctx context.Context, c *telegram.Context) error { return c.Answer(ctx, "done") })

	cb := telegram.Update{ID: 1, CallbackQuery: &telegram.CallbackQuery{ID: "q1", Data: "ok"}}
	if err := r.HandleUpdate(t.Context(), bot, cb); err != nil || f.count("answerCallbackQuery") != 1 {
		t.Errorf("answer = %v, calls %d", err, f.count("answerCallbackQuery"))
	}
}
