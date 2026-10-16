package vukem

import (
	"bytes"
	"context"
	"flag"
	"strings"
	"testing"
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
