package vukem

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/logging/telegram"
)

// TelegramConfig is read from TELEGRAM_* variables.
type TelegramConfig struct {
	BotToken config.Secret `env:"TELEGRAM_BOT_TOKEN"`
	ChatID   string        `env:"TELEGRAM_CHAT_ID"`
	// MinLevel is debug, info, warn or error (default error).
	MinLevel string `env:"TELEGRAM_MIN_LEVEL" default:"error"`
	ThreadID int    `env:"TELEGRAM_THREAD_ID"`
	// Log forwards log records at MinLevel and above to the chat (default
	// true); false keeps app.Telegram for explicit Send and Contact only.
	Log bool `env:"TELEGRAM_LOG" default:"true"`
	// RateLimit caps forwarded log records per minute (0: no cap).
	RateLimit int `env:"TELEGRAM_RATE_LIMIT"`
}

// WithTelegram adjusts the Telegram handler config after it is read from
// TELEGRAM_* (for example Filter, OnError, ChatIDs or Silent). It has no
// effect unless TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are set.
func WithTelegram(fn func(cfg *telegram.Config)) Option {
	return func(o *options) error { o.telegram = append(o.telegram, fn); return nil }
}

// telegramHandler returns the Telegram handler when the bot token and chat
// are configured, or nil (logging to Telegram is optional), and whether
// log records are forwarded to it.
func telegramHandler(
	src *config.Source,
	cfg *Config,
	configure []func(*telegram.Config),
) (*telegram.Handler, bool, error) {
	tc, err := config.Load[TelegramConfig](src)
	if err != nil {
		return nil, false, err
	}

	if tc.BotToken.IsZero() || tc.ChatID == "" {
		return nil, false, nil
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(tc.MinLevel))); err != nil {
		return nil, false, fmt.Errorf("vukem: TELEGRAM_MIN_LEVEL: %w", err)
	}

	if tc.RateLimit < 0 {
		return nil, false, fmt.Errorf("vukem: TELEGRAM_RATE_LIMIT must be >= 0, got %d", tc.RateLimit)
	}

	tg := telegram.Config{
		BotToken: tc.BotToken, ChatID: tc.ChatID, MinLevel: level, ThreadID: tc.ThreadID,
		AppName: cfg.Name, Env: string(cfg.Env), RateLimit: tc.RateLimit, RatePeriod: time.Minute,
	}

	for _, fn := range configure {
		fn(&tg)
	}

	return telegram.NewHandler(tg), tc.Log, nil
}
