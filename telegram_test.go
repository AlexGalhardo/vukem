package vukem

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/AlexGalhardo/vukem/logging/telegram"
)

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
