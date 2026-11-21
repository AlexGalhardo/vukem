package telegram

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/AlexGalhardo/vukem/config"
)

// SecretHeader is the header Telegram sends the webhook secret token in.
const SecretHeader = "X-Telegram-Bot-Api-Secret-Token" //nolint:gosec // a header name, not a credential

// WebhookOptions tune WebhookHandler.
type WebhookOptions struct {
	// MaxBodyBytes bounds an update (default 1 MiB).
	MaxBodyBytes int64
	// HandlerTimeout bounds the handler, which runs detached from the request
	// (default 30 seconds).
	HandlerTimeout time.Duration
}

// WebhookHandler returns the http.Handler Telegram posts updates to. Mount it
// on a path that is hard to guess as well, for example with the bot id.
//
// Requests without the exact secret token are refused with 401 before the
// body is read. secret must be 1 to 256 characters of A-Z, a-z, 0-9, _ and -,
// the set Telegram allows; an empty or invalid one is an error, so a webhook
// can never be open by mistake.
//
// A valid update is always answered 200, even when the handler fails: the
// failure is logged and Telegram does not retry an update the bot cannot
// handle.
func (b *Bot) WebhookHandler(h UpdateHandler, secret config.Secret, opts WebhookOptions) (http.Handler, error) {
	if !validSecret(secret.Reveal()) {
		return nil, errors.New("telegram: the webhook secret must be 1 to 256 characters of A-Z, a-z, 0-9, _ and -")
	}

	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = 1 << 20
	}

	if opts.HandlerTimeout <= 0 {
		opts.HandlerTimeout = 30 * time.Second
	}

	want := sha256.Sum256([]byte(secret.Reveal()))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		// Hash both sides so the comparison is constant time whatever the
		// length of what the caller sent.
		got := sha256.Sum256([]byte(r.Header.Get(SecretHeader)))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)

			return
		}

		if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
			http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)

			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, opts.MaxBodyBytes))
		if err != nil {
			status := http.StatusBadRequest
			if _, big := errors.AsType[*http.MaxBytesError](err); big {
				status = http.StatusRequestEntityTooLarge
			}

			http.Error(w, "bad request", status)

			return
		}

		var u Update
		if err := json.Unmarshal(body, &u); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)

			return
		}

		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), opts.HandlerTimeout)
		defer cancel()

		if err := h.HandleUpdate(ctx, b, u); err != nil && !errors.Is(err, ErrNoHandler) {
			b.cfg.Logger.ErrorContext(ctx, "telegram: handler failed", "update_id", u.ID, "err", b.redact(err))
		}

		w.WriteHeader(http.StatusOK)
	}), nil
}

func validSecret(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}

	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}

	return true
}
