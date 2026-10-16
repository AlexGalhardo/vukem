package vukem

import (
	"fmt"
	"log/slog"
	"strings"

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
}

// telegramHandler returns the Telegram handler when the bot token and chat
// are configured, or nil (logging to Telegram is optional).
func telegramHandler(src *config.Source, cfg *Config) (*telegram.Handler, error) {
	tc, err := config.Load[TelegramConfig](src)
	if err != nil {
		return nil, err
	}

	if tc.BotToken.IsZero() || tc.ChatID == "" {
		return nil, nil //nolint:nilnil // not configured: no handler
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(tc.MinLevel))); err != nil {
		return nil, fmt.Errorf("vukem: TELEGRAM_MIN_LEVEL: %w", err)
	}

	return telegram.NewHandler(telegram.Config{
		BotToken: tc.BotToken, ChatID: tc.ChatID, MinLevel: level, ThreadID: tc.ThreadID,
		AppName: cfg.Name, Env: string(cfg.Env),
	}), nil
}
