// Package telegram is a log/slog handler that sends records to Telegram
// chats, ported from the maintainer's TypeScript telegram-logger. Messages
// are queued and sent in the background, so logging never blocks or fails
// a request.
//
//	tg := telegram.NewHandler(telegram.Config{BotToken: token, ChatID: "-100123", MinLevel: slog.LevelError})
//	log := slog.New(slog.NewMultiHandler(base, tg))
//	defer tg.Close(ctx)
//
// Without a bot token and chat id the [Handler] is disabled, so apps can
// always install it; the bot token is a config.Secret and never appears
// in errors.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#telegram-logging.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/AlexGalhardo/vukem/config"
)

// Custom levels for Success and Fatal helpers.
const (
	LevelSuccess slog.Level = 2
	LevelFatal   slog.Level = 12
)

// MaxLength is Telegram's message limit.
const MaxLength = 4096

// Config configures the handler. Without BotToken and ChatID the handler
// is disabled (Enabled reports false), so it can always be installed.
type Config struct {
	BotToken config.Secret
	// ChatID is the default chat; ChatIDs routes levels to other chats.
	ChatID  string
	ChatIDs map[slog.Level]string
	// MinLevel filters records (default: error).
	MinLevel slog.Level
	AppName  string
	Env      string
	// ThreadID posts into a forum topic.
	ThreadID int
	// Silent sends without notification sound.
	Silent bool
	// QueueSize bounds pending messages (default 100); when full, new
	// messages are dropped and reported to OnError.
	QueueSize int
	// Timeout bounds each request (default 5s).
	Timeout time.Duration
	// MaxRetryAfter caps the wait for one retry after HTTP 429 (default 10s).
	MaxRetryAfter time.Duration
	// OnError is told about delivery failures (never contains the token).
	OnError func(error)
	// BaseURL defaults to https://api.telegram.org (tests use a fake).
	BaseURL string
	// HTTP is the client (default: Timeout per request).
	HTTP *http.Client
}

type message struct {
	chat string
	text string
}

// state is shared by a handler and its WithAttrs/WithGroup clones.
type state struct {
	cfg     Config
	queue   chan message
	wg      sync.WaitGroup
	mu      sync.RWMutex
	closed  bool
	enabled bool
	now     func() time.Time
}

// Handler implements slog.Handler.
type Handler struct {
	s      *state
	attrs  []slog.Attr
	groups []string
}

// ErrQueueFull is reported when a message is dropped.
var ErrQueueFull = errors.New("telegram: queue full, message dropped")

// NewHandler returns a handler and starts its sender goroutine (when
// enabled); call Close to drain it.
func NewHandler(cfg Config) *Handler {
	if cfg.MinLevel == 0 && cfg.ChatIDs == nil {
		cfg.MinLevel = slog.LevelError
	}

	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 100
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}

	if cfg.MaxRetryAfter <= 0 {
		cfg.MaxRetryAfter = 10 * time.Second
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.telegram.org"
	}

	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: cfg.Timeout}
	}

	if cfg.OnError == nil {
		cfg.OnError = func(error) {}
	}

	s := &state{cfg: cfg, queue: make(chan message, cfg.QueueSize), now: time.Now}
	s.enabled = !cfg.BotToken.IsZero() && (cfg.ChatID != "" || len(cfg.ChatIDs) > 0)

	if s.enabled {
		s.wg.Go(s.run)
	}

	return &Handler{s: s}
}

// Enabled implements slog.Handler.
func (h *Handler) Enabled(_ context.Context, l slog.Level) bool {
	return h.s.enabled && l >= h.s.cfg.MinLevel
}

// Handle implements slog.Handler: it formats r and queues it.
func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	if !h.Enabled(context.Background(), r.Level) {
		return nil
	}

	attrs := append([]slog.Attr(nil), h.attrs...)

	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, h.qualify(a))
		return true
	})

	h.s.enqueue(h.s.chatFor(r.Level), format(h.s.cfg, r.Level, r.Time, r.Message, attrs))

	return nil
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append([]slog.Attr(nil), h.attrs...)

	for _, a := range attrs {
		c.attrs = append(c.attrs, h.qualify(a))
	}

	return &c
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	c := *h
	c.groups = append(append([]string(nil), h.groups...), name)

	return &c
}

func (h *Handler) qualify(a slog.Attr) slog.Attr {
	if len(h.groups) > 0 {
		a.Key = strings.Join(h.groups, ".") + "." + a.Key
	}

	return a
}

// Send queues a pre-formatted HTML message for the default chat (the
// caller escapes user content).
func (h *Handler) Send(_ context.Context, htmlText string) {
	if h.s.enabled {
		h.s.enqueue(h.s.cfg.ChatID, truncate(htmlText))
	}
}

// Contact queues a contact-form notification (never filtered by level);
// every field is escaped.
func (h *Handler) Contact(_ context.Context, fields map[string]string) {
	if !h.s.enabled {
		return
	}

	var b strings.Builder

	b.WriteString("📬 <b>New contact</b>")
	writeHeader(&b, h.s.cfg)

	var body strings.Builder

	for _, k := range slices.Sorted(maps.Keys(fields)) {
		fmt.Fprintf(&body, "%s: %s\n", k, fields[k])
	}

	h.s.enqueue(h.s.cfg.ChatID, finish(b.String(), body.String()))
}

// Close stops accepting messages and waits for queued ones until ctx is
// done.
func (h *Handler) Close(ctx context.Context) error {
	s := h.s

	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.queue)
	}
	s.mu.Unlock()

	done := make(chan struct{})

	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("telegram: messages still queued: %w", ctx.Err())
	}
}

func (s *state) chatFor(l slog.Level) string {
	if c, ok := s.cfg.ChatIDs[l]; ok {
		return c
	}

	return s.cfg.ChatID
}

func (s *state) enqueue(chat, text string) {
	if chat == "" {
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return
	}

	select {
	case s.queue <- message{chat: chat, text: text}:
	default:
		s.cfg.OnError(ErrQueueFull)
	}
}

func (s *state) run() {
	for m := range s.queue {
		if err := s.deliver(m); err != nil {
			s.cfg.OnError(err)
		}
	}
}

// deliver posts m, retrying once after a 429 with a capped retry_after.
func (s *state) deliver(m message) error {
	payload := map[string]any{"chat_id": m.chat, "text": m.text, "parse_mode": "HTML", "disable_web_page_preview": true}
	if s.cfg.ThreadID != 0 {
		payload["message_thread_id"] = s.cfg.ThreadID
	}

	if s.cfg.Silent {
		payload["disable_notification"] = true
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	for attempt := range 2 {
		wait, err := s.post(body)
		if err == nil || wait == 0 || attempt == 1 {
			return err
		}

		time.Sleep(
			min(wait, s.cfg.MaxRetryAfter),
		) //nolint:forbidigo // honour Telegram's retry_after in the sender goroutine
	}

	return nil
}

// apiError never includes the request URL, which contains the token.
type apiError struct {
	Status      int
	Description string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("telegram: sendMessage %d: %s", e.Status, e.Description)
}

func (s *state) post(body []byte) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout)
	defer cancel()

	url := s.cfg.BaseURL + "/bot" + s.cfg.BotToken.Reveal() + "/sendMessage"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("telegram: build request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.cfg.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("telegram: request failed: %w", redact(err, s.cfg.BotToken.Reveal()))
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode == http.StatusOK {
		return 0, nil
	}

	var out struct {
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}

	_ = json.Unmarshal(data, &out)

	var wait time.Duration
	if resp.StatusCode == http.StatusTooManyRequests {
		wait = time.Duration(max(out.Parameters.RetryAfter, 1)) * time.Second
	}

	return wait, &apiError{Status: resp.StatusCode, Description: out.Description}
}

// redact removes the token from transport errors (url.Error includes the URL).
func redact(err error, token string) error {
	if token == "" {
		return err
	}

	return errors.New(strings.ReplaceAll(err.Error(), token, "[redacted]"))
}

var levelInfo = map[slog.Level][2]string{ //nolint:gochecknoglobals // read-only table
	slog.LevelDebug: {"🐛", "DEBUG"},
	slog.LevelInfo:  {"ℹ️", "INFO"},
	LevelSuccess:    {"✅", "SUCCESS"},
	slog.LevelWarn:  {"⚠️", "WARN"},
	slog.LevelError: {"🚨", "ERROR"},
	LevelFatal:      {"💀", "FATAL"},
}

func label(l slog.Level) (string, string) {
	if v, ok := levelInfo[l]; ok {
		return v[0], v[1]
	}

	return "📝", l.String()
}

func writeHeader(b *strings.Builder, cfg Config) {
	if cfg.AppName != "" {
		b.WriteString(" — " + html.EscapeString(cfg.AppName))
	}

	if cfg.Env != "" {
		b.WriteString(" <i>(" + html.EscapeString(cfg.Env) + ")</i>")
	}
}

// format builds the HTML message: a fixed header and the escaped body in
// a <pre> block, so truncation can never cut a tag.
func format(cfg Config, l slog.Level, t time.Time, msg string, attrs []slog.Attr) string {
	emoji, name := label(l)

	var head strings.Builder

	head.WriteString(emoji + " <b>" + name + "</b>")
	writeHeader(&head, cfg)

	if !t.IsZero() {
		head.WriteString("\n<i>" + t.UTC().Format(time.RFC3339) + "</i>")
	}

	var body strings.Builder

	body.WriteString(msg)

	for _, a := range attrs {
		writeAttr(&body, "", a)
	}

	return finish(head.String(), body.String())
}

func writeAttr(b *strings.Builder, prefix string, a slog.Attr) {
	v := a.Value.Resolve()
	key := prefix + a.Key

	if v.Kind() == slog.KindGroup {
		for _, ga := range v.Group() {
			writeAttr(b, key+".", ga)
		}

		return
	}

	if err, ok := v.Any().(error); ok {
		b.WriteString("\n" + key + ": " + err.Error())

		for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
			b.WriteString("\n  caused by: " + e.Error())
		}

		return
	}

	b.WriteString("\n" + key + ": " + v.String())
}

// finish joins the header and the escaped, truncated body.
func finish(head, body string) string {
	const open, closeTag = "\n\n<pre>", "</pre>"

	room := MaxLength - utf8.RuneCountInString(head) - len(open) - len(closeTag)
	escaped := html.EscapeString(body)

	if utf8.RuneCountInString(escaped) > room {
		escaped = cutRunes(escaped, max(room-1, 0))
		// Do not leave half an entity ("&am").
		if i := strings.LastIndexByte(escaped, '&'); i >= 0 && !strings.Contains(escaped[i:], ";") {
			escaped = escaped[:i]
		}

		escaped += "…"
	}

	if escaped == "" {
		return head
	}

	return head + open + escaped + closeTag
}

func truncate(s string) string {
	if utf8.RuneCountInString(s) <= MaxLength {
		return s
	}

	return cutRunes(s, MaxLength-1) + "…"
}

func cutRunes(s string, n int) string {
	i := 0

	for pos := range s {
		if i == n {
			return s[:pos]
		}

		i++
	}

	return s
}
