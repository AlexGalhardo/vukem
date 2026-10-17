package telegram_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/logging/telegram"
)

func ExampleNewHandler() {
	// A fake Bot API stands in for https://api.telegram.org.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID string `json:"chat_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fmt.Println("sent to chat", body.ChatID)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()

	tg := telegram.NewHandler(telegram.Config{
		BotToken: config.NewSecret("123:TOKEN"),
		ChatID:   "-100123",
		MinLevel: slog.LevelError,
		BaseURL:  api.URL,
	})
	log := slog.New(tg)

	log.Info("ignored: below MinLevel")
	log.Error("payment failed", "order_id", 42)

	// Close flushes queued messages before shutdown.
	_ = tg.Close(context.Background())
	// Output: sent to chat -100123
}
