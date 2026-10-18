package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/AlexGalhardo/vukem/config"
)

// fakeAPI records sendMessage calls; status picks the response per call.
type fakeAPI struct {
	*httptest.Server

	mu     sync.Mutex
	got    []map[string]any
	paths  []string
	calls  atomic.Int32
	status func(n int32) (int, string)
}

func newFake(t *testing.T, status func(n int32) (int, string)) *fakeAPI {
	t.Helper()

	f := &fakeAPI{status: status}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := f.calls.Add(1)

		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)

		f.mu.Lock()
		f.got = append(f.got, body)
		f.paths = append(f.paths, r.URL.Path)
		f.mu.Unlock()

		code, resp := http.StatusOK, `{"ok":true}`
		if f.status != nil {
			code, resp = f.status(n)
		}

		w.WriteHeader(code)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(f.Close)

	return f
}

func (f *fakeAPI) messages() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]map[string]any(nil), f.got...)
}

func cfg(f *fakeAPI) Config {
	return Config{
		BotToken: config.NewSecret("123:SECRET"), ChatID: "-100", AppName: "Shop", Env: "production",
		BaseURL: f.URL, MinLevel: slog.LevelWarn,
	}
}

func TestFormatGolden(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	err := fmt.Errorf("charge failed: %w", errors.New("card declined"))

	got := format(Config{AppName: "Shop <prod>", Env: "production"}, slog.LevelError, at, "Payment <failed> & retried",
		[]slog.Attr{
			slog.Int("order_id", 42),
			slog.String("note", "<script>alert(1)</script>"),
			slog.Any("err", err),
			slog.Group("user", slog.String("email", "a@b.co")),
		})

	want := "🚨 <b>ERROR</b> — Shop &lt;prod&gt; <i>(production)</i>\n<i>2026-10-16T12:00:00Z</i>\n\n<pre>" +
		"Payment &lt;failed&gt; &amp; retried\norder_id: 42\nnote: &lt;script&gt;alert(1)&lt;/script&gt;\n" +
		"err: charge failed: card declined\n  caused by: card declined\nuser.email: a@b.co</pre>"
	if got != want {
		t.Fatalf("format =\n%s\nwant\n%s", got, want)
	}

	for l, w := range map[slog.Level]string{
		slog.LevelDebug: "🐛 <b>DEBUG</b>", slog.LevelInfo: "ℹ️ <b>INFO</b>", LevelSuccess: "✅ <b>SUCCESS</b>",
		slog.LevelWarn: "⚠️ <b>WARN</b>", LevelFatal: "💀 <b>FATAL</b>", slog.Level(7): "📝 <b>WARN+3</b>",
	} {
		if got := format(Config{}, l, time.Time{}, "", nil); got != w {
			t.Errorf("level %v = %q, want %q", l, got, w)
		}
	}
}

func TestTruncation(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("é&", 5000)
	got := format(Config{AppName: "A"}, slog.LevelError, time.Time{}, long, nil)

	if n := utf8.RuneCountInString(got); n > MaxLength {
		t.Fatalf("length = %d", n)
	}

	if !strings.HasSuffix(got, "…</pre>") || strings.Contains(got[len(got)-12:], "&am…") {
		t.Fatalf("tail = %q", got[len(got)-20:])
	}

	if s := truncate(strings.Repeat("x", 5000)); utf8.RuneCountInString(s) != MaxLength {
		t.Fatal("Send truncation")
	}

	// Telegram counts UTF-16 units: an emoji is two.
	emoji := format(Config{AppName: "A"}, slog.LevelError, time.Time{}, strings.Repeat("😀", 3000), nil)
	if n := utf16Len(emoji); n > MaxLength {
		t.Fatalf("emoji message has %d UTF-16 units, want <= %d", n, MaxLength)
	}
}

func TestHandlerSendsAndRoutes(t *testing.T) {
	t.Parallel()

	f := newFake(t, nil)
	c := cfg(f)
	c.ChatIDs = map[slog.Level]string{slog.LevelError: "-999"}
	c.ThreadID, c.Silent = 7, true

	h := NewHandler(c)
	log := slog.New(h).With("request_id", "r1").WithGroup("db")

	log.Info("ignored: below MinLevel")
	log.Warn("slow query", "ms", 1200)
	log.Error("down")
	h.Contact(t.Context(), map[string]string{"name": "Ana <b>", "message": "Hi"})
	h.Send(t.Context(), "<b>deploy</b> done")

	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	msgs := f.messages()
	if len(msgs) != 4 {
		t.Fatalf("sent %d messages: %v", len(msgs), msgs)
	}

	warn, errMsg, contact, sent := msgs[0], msgs[1], msgs[2], msgs[3]

	if warn["chat_id"] != "-100" || errMsg["chat_id"] != "-999" || warn["parse_mode"] != "HTML" ||
		warn["message_thread_id"] != 7.0 || warn["disable_notification"] != true {
		t.Fatalf("routing = %v / %v", warn, errMsg)
	}

	if text, _ := warn["text"].(string); !strings.Contains(text, "request_id: r1\ndb.ms: 1200") {
		t.Fatalf("attrs = %q", text)
	}

	if text, _ := contact["text"].(string); !strings.Contains(text, "name: Ana &lt;b&gt;") {
		t.Fatalf("contact = %q", text)
	}

	if sent["text"] != "<b>deploy</b> done" || f.paths[0] != "/bot123:SECRET/sendMessage" {
		t.Fatalf("send = %v %v", sent, f.paths)
	}

	// After Close nothing is queued.
	log.Error("late")

	if len(f.messages()) != 4 {
		t.Fatal("message sent after Close")
	}
}

func TestRetryAfter429AndErrors(t *testing.T) {
	t.Parallel()

	f := newFake(t, func(n int32) (int, string) {
		if n == 1 {
			return http.StatusTooManyRequests, `{"ok":false,"description":"Too Many Requests","parameters":{"retry_after":30}}`
		}

		if n == 3 {
			return http.StatusBadRequest, `{"ok":false,"description":"chat not found"}`
		}

		return http.StatusOK, `{"ok":true}`
	})

	var (
		mu   sync.Mutex
		errs []error
	)

	c := cfg(f)
	c.MaxRetryAfter = 10 * time.Millisecond // capped: never wait Telegram's 30s
	c.OnError = func(err error) { mu.Lock(); errs = append(errs, err); mu.Unlock() }

	h := NewHandler(c)
	log := slog.New(h)

	start := time.Now()

	log.Error("first")  // 429 then OK on retry
	log.Error("second") // 400

	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if f.calls.Load() != 3 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "chat not found") ||
		time.Since(start) > 5*time.Second {
		t.Fatalf("calls = %d errs = %v", f.calls.Load(), errs)
	}
}

func TestSecurity_A09_TelegramTokenRedacted(t *testing.T) {
	t.Parallel()

	var got error

	c := Config{
		BotToken: config.NewSecret("123:SUPERSECRET"), ChatID: "-1", BaseURL: "http://127.0.0.1:1",
		Timeout: time.Second, OnError: func(err error) { got = err },
	}

	h := NewHandler(c)
	slog.New(h).Error("boom")

	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got == nil || strings.Contains(got.Error(), "SUPERSECRET") || !strings.Contains(got.Error(), "[redacted]") {
		t.Fatalf("error = %v", got)
	}

	if redact(errors.New("x"), "") == nil {
		t.Fatal("redact")
	}
}

func TestDisabledAndQueueFull(t *testing.T) {
	t.Parallel()

	off := NewHandler(Config{ChatID: "-1"}) // no token
	if off.Enabled(context.Background(), LevelFatal) {
		t.Fatal("enabled without a token")
	}

	off.Send(t.Context(), "x")
	off.Contact(t.Context(), map[string]string{"a": "b"})
	_ = off.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelError, "x", 0))

	if err := off.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	block := make(chan struct{})
	f := newFake(t, func(int32) (int, string) { <-block; return http.StatusOK, `{"ok":true}` })

	var dropped atomic.Int32

	c := cfg(f)
	c.QueueSize = 1
	c.OnError = func(err error) {
		if errors.Is(err, ErrQueueFull) {
			dropped.Add(1)
		}
	}

	h := NewHandler(c)
	for range 5 {
		slog.New(h).Error("burst")
	}

	time.Sleep(50 * time.Millisecond) //nolint:forbidigo // let the sender pick the first message

	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := h.Close(short); err == nil {
		t.Fatal("Close returned with a blocked request")
	}

	close(block)

	if dropped.Load() == 0 {
		t.Fatal("no message dropped with a full queue")
	}

	h.s.enqueue("", "x") // empty chat ignored
}

func TestMinLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cfg       Config
		info, err bool
	}{
		{"unset defaults to error", Config{}, false, true},
		{"explicit info is honoured", Config{MinLevel: slog.LevelInfo, MinLevelSet: true}, true, true},
		{"explicit warn", Config{MinLevel: slog.LevelWarn}, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.cfg.BotToken, tt.cfg.ChatID = config.NewSecret("1:x"), "-1"
			h := NewHandler(tt.cfg)
			t.Cleanup(func() { _ = h.Close(context.Background()) })

			if got := h.Enabled(t.Context(), slog.LevelInfo); got != tt.info {
				t.Errorf("Enabled(info) = %v, want %v", got, tt.info)
			}

			if got := h.Enabled(t.Context(), slog.LevelError); got != tt.err {
				t.Errorf("Enabled(error) = %v, want %v", got, tt.err)
			}
		})
	}
}

func TestTruncateHTML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in, want string
		limit          int
	}{
		{"short is unchanged", "<b>hi</b>", "<b>hi</b>", 20},
		{"plain text", "abcdefgh", "abcd…", 5},
		{"closes open tags", "<b>bold <i>both</i> more text</b>", "<b>bold …</b>", 13},
		{"never cuts inside a tag", `<a href="https://example.com">link</a>`, "…", 10},
		{"never cuts inside an entity", "a&amp;b&amp;c", "a&amp;…", 7},
		{"nested tags closed in order", "<b><i>abcdefghijklmnop</i></b>", "<b><i>abcd…</i></b>", 19},
		{"closed tags are not reclosed", "<b>a</b>bcdefghij", "<b>a</b>bc…", 11},
		{"self-closing tags are not reopened", "<b>a<br/>bcdefghij</b>", "<b>a<br/>bc…</b>", 16},
		{"emoji count as two UTF-16 units", "ab😀😀😀😀", "ab😀…", 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := truncateHTML(tt.in, tt.limit)
			if got != tt.want {
				t.Errorf("truncateHTML(%q, %d) = %q, want %q", tt.in, tt.limit, got, tt.want)
			}

			if n := utf16Len(got); n > tt.limit {
				t.Errorf("truncateHTML(%q, %d) has %d UTF-16 units", tt.in, tt.limit, n)
			}
		})
	}
}

func TestSendKeepsHTMLValid(t *testing.T) {
	t.Parallel()

	f := newFake(t, nil)
	h := NewHandler(cfg(f))

	if err := h.SendSync(t.Context(), "<b>"+strings.Repeat("x", 5000)+"</b>"); err != nil {
		t.Fatal(err)
	}

	text, _ := f.messages()[0]["text"].(string)
	if n := utf16Len(text); n > MaxLength {
		t.Fatalf("SendSync sent %d UTF-16 units", n)
	}

	if err := h.SendSync(t.Context(), strings.Repeat("😀", 3000)); err != nil {
		t.Fatal(err)
	}

	if text, _ := f.messages()[1]["text"].(string); utf16Len(text) > MaxLength {
		t.Fatalf("SendSync of emoji sent %d UTF-16 units, want <= %d", utf16Len(text), MaxLength)
	}

	if !strings.HasPrefix(text, "<b>") || !strings.HasSuffix(text, "…</b>") {
		t.Fatalf("SendSync sent %q...%q", text[:10], text[len(text)-10:])
	}
}

func TestDisabledHandlerIsSafe(t *testing.T) {
	t.Parallel()

	h := NewHandler(Config{})
	if h.Configured() {
		t.Fatal("Configured() = true without token and chat")
	}

	h.Send(t.Context(), "x")
	h.Contact(t.Context(), map[string]string{"a": "b"})

	if err := h.SendSync(t.Context(), "x"); !errors.Is(err, ErrDisabled) {
		t.Errorf("SendSync = %v, want ErrDisabled", err)
	}

	if err := h.Close(t.Context()); err != nil {
		t.Errorf("Close = %v", err)
	}
}
