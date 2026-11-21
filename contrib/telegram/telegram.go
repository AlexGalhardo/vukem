package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
)

// Config configures a Bot. Token is required.
type Config struct {
	// Token is the bot token from BotFather.
	Token config.Secret
	// BaseURL defaults to https://api.telegram.org; tests point it at a fake.
	BaseURL string
	// HTTP is the client for the Bot API (default: 60 second timeout).
	HTTP *http.Client
	// Logger receives failures that cannot be returned (default: discarded).
	Logger *slog.Logger
	// MaxResponseBytes bounds a Bot API response (default 4 MiB).
	MaxResponseBytes int64
	// Retries is how many times a call answered 429 is retried after the
	// retry_after the API asks for (default 2; negative disables).
	Retries int
	// MaxRetryAfter caps how long a call waits for a 429 (default 30s); a
	// longer retry_after is returned as the error instead.
	MaxRetryAfter time.Duration
	// Sleep waits d or until ctx ends (default: a timer). Tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Bot is a Bot API client.
type Bot struct {
	cfg Config
}

// New returns a Bot.
func New(cfg Config) (*Bot, error) {
	if cfg.Token.IsZero() {
		return nil, errors.New("telegram: Config.Token is required")
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.telegram.org"
	}

	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")

	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 60 * time.Second}
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = 4 << 20
	}

	if cfg.Retries == 0 {
		cfg.Retries = 2
	}

	if cfg.MaxRetryAfter <= 0 {
		cfg.MaxRetryAfter = 30 * time.Second
	}

	if cfg.Sleep == nil {
		cfg.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()

			select {
			case <-t.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return &Bot{cfg: cfg}, nil
}

// APIError is an error answer of the Bot API.
type APIError struct {
	// Code is the API error code, usually the HTTP status.
	Code        int
	Description string
	// RetryAfter is set on 429: how long the API asks to wait.
	RetryAfter time.Duration
}

// Error implements error.
func (e *APIError) Error() string {
	return "telegram: " + strconv.Itoa(e.Code) + ": " + e.Description
}

// Update is an incoming update. Only the kinds the router handles are decoded.
type Update struct {
	ID            int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	EditedMessage *Message       `json:"edited_message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// Message is a message.
type Message struct {
	ID   int64  `json:"message_id"`
	From *User  `json:"from,omitempty"`
	Chat Chat   `json:"chat"`
	Date int64  `json:"date"`
	Text string `json:"text,omitempty"`
}

// User is a Telegram user or bot.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username,omitempty"`
}

// Chat is a chat.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// CallbackQuery is a press on an inline keyboard button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// SendMessage are the parameters of sendMessage.
type SendMessage struct {
	ChatID      int64  `json:"chat_id"`
	Text        string `json:"text"`
	ParseMode   string `json:"parse_mode,omitempty"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

// Call invokes a Bot API method with params (a struct or map; nil for none)
// and decodes the result into result (nil to ignore it). A 429 answer is
// retried after its retry_after, up to Config.Retries times.
func (b *Bot) Call(ctx context.Context, method string, params, result any) error {
	body := []byte("{}")

	if params != nil {
		var err error
		if body, err = json.Marshal(params); err != nil {
			return fmt.Errorf("telegram: %s: encode params: %w", method, err)
		}
	}

	for attempt := 0; ; attempt++ {
		err := b.once(ctx, method, body, result)

		api, ok := errors.AsType[*APIError](err)
		if !ok || api.Code != http.StatusTooManyRequests || attempt >= b.cfg.Retries ||
			api.RetryAfter > b.cfg.MaxRetryAfter {
			return err
		}

		if serr := b.cfg.Sleep(ctx, max(api.RetryAfter, time.Second)); serr != nil {
			return serr
		}
	}
}

func (b *Bot) once(ctx context.Context, method string, body []byte, result any) error {
	url := b.cfg.BaseURL + "/bot" + b.cfg.Token.Reveal() + "/" + method

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: %s: build request", method)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := b.cfg.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: %s: request failed: %w", method, b.redact(err))
	}

	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, b.cfg.MaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("telegram: %s: read response: %w", method, b.redact(err))
	}

	if int64(len(data)) > b.cfg.MaxResponseBytes {
		return fmt.Errorf("telegram: %s: response too large", method)
	}

	var out struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}

	if err := json.Unmarshal(data, &out); err != nil {
		return &APIError{Code: resp.StatusCode, Description: "unreadable response"}
	}

	if !out.OK {
		code := out.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}

		return &APIError{
			Code: code, Description: b.scrub(out.Description),
			RetryAfter: time.Duration(out.Parameters.RetryAfter) * time.Second,
		}
	}

	if result != nil {
		if err := json.Unmarshal(out.Result, result); err != nil {
			return fmt.Errorf("telegram: %s: decode result: %w", method, err)
		}
	}

	return nil
}

// scrub removes the token from text that came from outside.
func (b *Bot) scrub(s string) string {
	if tok := b.cfg.Token.Reveal(); tok != "" {
		return strings.ReplaceAll(s, tok, "[redacted]")
	}

	return s
}

// redact returns err without the token: a transport error carries the URL,
// which holds it. Context errors keep their identity.
func (b *Bot) redact(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	return errors.New(b.scrub(err.Error()))
}

// SendMessage sends a text message.
func (b *Bot) SendMessage(ctx context.Context, m SendMessage) (*Message, error) {
	var out Message
	if err := b.Call(ctx, "sendMessage", m, &out); err != nil {
		return nil, err
	}

	return &out, nil
}

// AnswerCallbackQuery acknowledges a button press; text is shown to the user
// when not empty.
func (b *Bot) AnswerCallbackQuery(ctx context.Context, id, text string) error {
	return b.Call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

// SetWebhook registers url with Telegram and the secret token it sends back
// in the X-Telegram-Bot-Api-Secret-Token header (1 to 256 characters of
// A-Z, a-z, 0-9, _ and -).
func (b *Bot) SetWebhook(ctx context.Context, url string, secret config.Secret) error {
	return b.Call(ctx, "setWebhook", map[string]any{
		"url": url, "secret_token": secret.Reveal(), "allowed_updates": allowedUpdates,
	}, nil)
}

// DeleteWebhook removes the webhook, which long polling needs.
func (b *Bot) DeleteWebhook(ctx context.Context) error {
	return b.Call(ctx, "deleteWebhook", nil, nil)
}

var allowedUpdates = []string{"message", "edited_message", "callback_query"}

// GetUpdates is the long polling call.
func (b *Bot) GetUpdates(ctx context.Context, offset int64, timeoutSeconds int) ([]Update, error) {
	var out []Update

	err := b.Call(ctx, "getUpdates", map[string]any{
		"offset": offset, "timeout": timeoutSeconds, "limit": 100, "allowed_updates": allowedUpdates,
	}, &out)

	return out, err
}
