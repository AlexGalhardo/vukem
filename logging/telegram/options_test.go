package telegram

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/config"
)

func TestFilterSkipsRecords(t *testing.T) {
	t.Parallel()

	f := newFake(t, nil)
	c := cfg(f)
	c.Filter = func(_ context.Context, r slog.Record) bool {
		alert := false

		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "alert" {
				alert = a.Value.Bool()
			}

			return true
		})

		return alert
	}

	h := NewHandler(c)
	log := slog.New(h)

	log.Error("noise")
	log.Error("page me", "alert", true)
	h.Send(t.Context(), "explicit") // Filter applies to log records only

	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, m := range f.messages() {
		text, _ := m["text"].(string)
		got = append(got, firstBodyLine(text))
	}

	if diff := cmp.Diff([]string{"page me", "explicit"}, got); diff != "" {
		t.Errorf("sent (-want +got):\n%s", diff)
	}
}

// firstBodyLine returns the log message of a formatted record, or text
// itself for a raw Send.
func firstBodyLine(text string) string {
	_, body, ok := strings.Cut(text, "<pre>")
	if !ok {
		return text
	}

	line, _, _ := strings.Cut(strings.TrimSuffix(body, "</pre>"), "\n")

	return line
}

func TestRateLimit(t *testing.T) {
	t.Parallel()

	f := newFake(t, nil)

	var (
		mu      sync.Mutex
		limited int
	)

	c := cfg(f)
	c.RateLimit = 2
	c.RatePeriod = time.Minute
	c.OnError = func(err error) {
		if errors.Is(err, ErrRateLimited) {
			mu.Lock()
			limited++
			mu.Unlock()
		}
	}

	h := NewHandler(c)

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.s.now = func() time.Time { return now }

	log := slog.New(h)
	for range 5 {
		log.Error("burst")
	}

	h.Send(t.Context(), "explicit") // not rate limited

	now = now.Add(time.Minute)

	log.Error("next window")

	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	if n := len(f.messages()); n != 4 {
		t.Errorf("sent %d messages, want 4 (2 burst + explicit + next window)", n)
	}

	mu.Lock()
	defer mu.Unlock()

	if limited != 1 {
		t.Errorf("ErrRateLimited reported %d times, want once per window", limited)
	}
}

func TestSendSyncAndContactSync(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  func(int32) (int, string)
		wantErr string
	}{
		{"ok", nil, ""},
		{"api error", func(int32) (int, string) {
			return http.StatusBadRequest, `{"ok":false,"description":"chat not found"}`
		}, "chat not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(t, tt.status)
			h := NewHandler(cfg(f))
			t.Cleanup(func() { _ = h.Close(context.Background()) })

			errs := []error{
				h.SendSync(t.Context(), "<b>hi</b>"),
				h.ContactSync(t.Context(), map[string]string{"name": "Ana"}),
			}

			for i, err := range errs {
				if tt.wantErr == "" && err != nil {
					t.Errorf("call %d = %v, want nil", i, err)
				}

				if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
					t.Errorf("call %d = %v, want %q", i, err, tt.wantErr)
				}
			}

			// Synchronous: the request already happened when the call returned.
			if n := f.calls.Load(); n != 2 {
				t.Errorf("API calls after return = %d, want 2", n)
			}
		})
	}
}

func TestSendSyncDisabled(t *testing.T) {
	t.Parallel()

	h := NewHandler(Config{ChatID: "-1"})

	if err := h.SendSync(t.Context(), "x"); !errors.Is(err, ErrDisabled) {
		t.Errorf("SendSync(disabled) = %v, want ErrDisabled", err)
	}

	if err := h.ContactSync(t.Context(), nil); !errors.Is(err, ErrDisabled) {
		t.Errorf("ContactSync(disabled) = %v, want ErrDisabled", err)
	}
}

func TestSendSyncHonoursContext(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	f := newFake(t, func(int32) (int, string) { <-block; return http.StatusOK, `{"ok":true}` })
	t.Cleanup(func() { close(block) })

	c := cfg(f)
	c.Timeout = time.Minute

	h := NewHandler(c)
	t.Cleanup(func() { _ = h.Close(context.Background()) })

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()

	err := h.SendSync(ctx, "x")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("SendSync(expired ctx) = %v, want context.DeadlineExceeded", err)
	}

	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("SendSync took %v with a 20ms context", d)
	}
}

// leakyTransport fails like net/http does, with the request URL (and so
// the bot token) inside the error.
type leakyTransport struct{}

func (leakyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, errors.New("dial " + r.URL.String() + ": connection refused")
}

func TestSecurity_A09_TelegramSyncErrorRedactsToken(t *testing.T) {
	t.Parallel()

	h := NewHandler(Config{
		BotToken: config.NewSecret("123:SUPERSECRET"), ChatID: "-1",
		HTTP: &http.Client{Transport: leakyTransport{}},
	})
	t.Cleanup(func() { _ = h.Close(context.Background()) })

	for name, err := range map[string]error{
		"SendSync":    h.SendSync(t.Context(), "x"),
		"ContactSync": h.ContactSync(t.Context(), map[string]string{"a": "b"}),
	} {
		if err == nil || strings.Contains(err.Error(), "SUPERSECRET") || !strings.Contains(err.Error(), "[redacted]") {
			t.Errorf("%s error = %v, want the token redacted", name, err)
		}
	}
}
