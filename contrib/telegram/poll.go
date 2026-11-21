package telegram

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// PollOptions tune Poll.
type PollOptions struct {
	// Timeout is the long polling wait in seconds (default 30).
	Timeout int
	// MinBackoff and MaxBackoff bound the wait after a failed poll (default
	// 1 second and 30 seconds); it doubles on every failure, with jitter.
	MinBackoff, MaxBackoff time.Duration
	// HandlerTimeout bounds each handler (default 30 seconds).
	HandlerTimeout time.Duration
}

// Poll receives updates with getUpdates until ctx ends, handing each to h in
// order. A webhook must not be set at the same time: call DeleteWebhook first.
// A failing poll is retried with exponential backoff; a handler error is
// logged and the update is not retried. Poll returns ctx.Err() when stopped
// and an error only for a rejected token (401).
func (b *Bot) Poll(ctx context.Context, h UpdateHandler, opts PollOptions) error {
	if opts.Timeout <= 0 {
		opts.Timeout = 30
	}

	if opts.MinBackoff <= 0 {
		opts.MinBackoff = time.Second
	}

	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = 30 * time.Second
	}

	if opts.HandlerTimeout <= 0 {
		opts.HandlerTimeout = 30 * time.Second
	}

	var (
		offset  int64
		backoff = opts.MinBackoff
	)

	for ctx.Err() == nil {
		updates, err := b.GetUpdates(ctx, offset, opts.Timeout)
		if err != nil {
			if ctx.Err() != nil {
				break
			}

			if api, ok := errors.AsType[*APIError](err); ok && (api.Code == 401 || api.Code == 404) {
				return err // the token is wrong: retrying cannot help
			}

			b.cfg.Logger.WarnContext(ctx, "telegram: poll failed", "err", b.redact(err), "retry_in", backoff)

			// Jitter keeps many bots from retrying in step.
			jitter := rand.N(backoff/2 + 1) //nolint:gosec // jitter, not a secret
			if b.cfg.Sleep(ctx, backoff/2+jitter) != nil {
				break
			}

			backoff = min(backoff*2, opts.MaxBackoff)

			continue
		}

		backoff = opts.MinBackoff

		for _, u := range updates {
			offset = max(offset, u.ID+1)

			hctx, cancel := context.WithTimeout(ctx, opts.HandlerTimeout)

			if err := h.HandleUpdate(hctx, b, u); err != nil && !errors.Is(err, ErrNoHandler) {
				b.cfg.Logger.ErrorContext(ctx, "telegram: handler failed", "update_id", u.ID, "err", b.redact(err))
			}

			cancel()
		}
	}

	return ctx.Err()
}
