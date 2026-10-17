package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/events"
)

// DefaultTolerance is how old a webhook may be (Stripe's own default).
const DefaultTolerance = 5 * time.Minute

// ErrSignature is returned for webhooks whose Stripe-Signature does not
// verify: missing, forged, for another secret, or older than the
// tolerance (replays).
var ErrSignature = errors.New("stripe: invalid webhook signature")

// Event is a webhook event.
type Event struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Created    int64  `json:"created"`
	Livemode   bool   `json:"livemode"`
	APIVersion string `json:"api_version"`
	// Account is the connected account (acct_...) a Connect event happened
	// on; "" for events on the platform account itself.
	Account string `json:"account,omitempty"`
	Data    struct {
		Object             json.RawMessage `json:"object"`
		PreviousAttributes json.RawMessage `json:"previous_attributes,omitempty"`
	} `json:"data"`
}

// Decode unmarshals the event's data object into T (CheckoutSession,
// Subscription, ...).
func Decode[T any](e Event) (T, error) {
	var v T
	err := json.Unmarshal(e.Data.Object, &v)

	return v, err
}

// ConstructEvent verifies the Stripe-Signature header of payload against
// secret (whsec_...) and returns the event. Any v1 signature may match
// (secret rotation sends several); the timestamp must be within tolerance
// of now, in both directions.
func ConstructEvent(payload []byte, header, secret string, tolerance time.Duration, now time.Time) (Event, error) {
	var (
		ts   int64
		sigs []string
	)

	for part := range strings.SplitSeq(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}

		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return Event{}, fmt.Errorf("%w: bad timestamp", ErrSignature)
			}

			ts = n
		case "v1":
			sigs = append(sigs, v)
		}
	}

	if ts == 0 || len(sigs) == 0 {
		return Event{}, fmt.Errorf("%w: no timestamp or v1 signature", ErrSignature)
	}

	age := now.Sub(time.Unix(ts, 0))
	if age > tolerance || age < -tolerance {
		return Event{}, fmt.Errorf("%w: timestamp outside the tolerance", ErrSignature)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(payload)
	want := mac.Sum(nil)

	matched := false

	for _, s := range sigs {
		got, err := hex.DecodeString(s)
		if err == nil && hmac.Equal(got, want) {
			matched = true
		}
	}

	if !matched {
		return Event{}, fmt.Errorf("%w: no matching signature", ErrSignature)
	}

	var e Event
	if err := json.Unmarshal(payload, &e); err != nil || e.ID == "" || e.Type == "" {
		return Event{}, fmt.Errorf("stripe: webhook payload is not an event: %w", err)
	}

	return e, nil
}

// constructEventAny verifies payload against each secret in turn and
// returns the first match; a verified payload that is not an event stops
// the search.
func constructEventAny(
	payload []byte,
	header string,
	secrets []string,
	tol time.Duration,
	now time.Time,
) (Event, error) {
	var err error

	for _, s := range secrets {
		var ev Event
		if ev, err = ConstructEvent(payload, header, s, tol, now); err == nil || !errors.Is(err, ErrSignature) {
			return ev, err
		}
	}

	return Event{}, err
}

// Received is dispatched on the events.Bus for every verified, first-seen
// webhook event; listen with events.On[stripe.Received] and switch on
// Event.Type.
type Received struct{ Event Event }

// WebhookConfig configures WebhookHandler.
type WebhookConfig struct {
	// Secret is the endpoint's signing secret (whsec_...).
	Secret string
	// Secrets are further signing secrets accepted besides Secret: a
	// platform endpoint and a Connect endpoint ("Events on connected
	// accounts") have different secrets even when both post to the same
	// URL. A payload must verify against one of them.
	Secrets []string
	// Store remembers processed event ids (cache.Store: memory, database or
	// Redis). Required: without it duplicates would be processed twice.
	Store cache.Store
	// Bus receives Received events.
	Bus events.Dispatcher
	// Tolerance defaults to DefaultTolerance.
	Tolerance time.Duration
	// MaxBody defaults to 1 MiB.
	MaxBody int64
	// Log reports rejected webhooks (default slog.Default()).
	Log *slog.Logger
	// Now is the clock (default time.Now).
	Now func() time.Time
}

const (
	stateProcessing = "processing"
	stateDone       = "done"
)

// WebhookHandler receives Stripe webhooks: it verifies the signature,
// claims the event id (a duplicate delivery of a processed event answers
// 200 without dispatching; one still in progress answers 409 so Stripe
// retries), dispatches Received and records success. A dispatch error
// releases the claim and answers 500, so Stripe redelivers. Mount it
// outside CSRF protection (Stripe is a server, not a browser).
func WebhookHandler(cfg WebhookConfig) (http.Handler, error) {
	var secrets []string
	if cfg.Secret != "" {
		secrets = append(secrets, cfg.Secret)
	}

	for _, s := range cfg.Secrets {
		if s == "" {
			return nil, errors.New("stripe: webhook Secrets has an empty secret")
		}

		secrets = append(secrets, s)
	}

	if len(secrets) == 0 || cfg.Store == nil || cfg.Bus == nil {
		return nil, errors.New("stripe: webhook needs Secret (or Secrets), Store and Bus")
	}

	if cfg.Tolerance == 0 {
		cfg.Tolerance = DefaultTolerance
	}

	if cfg.MaxBody == 0 {
		cfg.MaxBody = 1 << 20
	}

	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, cfg.MaxBody))
		if err != nil {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}

		ev, err := constructEventAny(payload, r.Header.Get("Stripe-Signature"), secrets, cfg.Tolerance, cfg.Now())
		if err != nil {
			cfg.Log.WarnContext(r.Context(), "stripe webhook rejected", "event", "stripe.webhook_rejected", "err", err)
			http.Error(w, "invalid signature", http.StatusBadRequest)

			return
		}

		key := "stripe:event:" + ev.ID
		ctx := r.Context()

		claimed, err := cfg.Store.Add(ctx, key, []byte(stateProcessing), 10*time.Minute)
		if err != nil {
			http.Error(w, "store unavailable", http.StatusServiceUnavailable) // fail closed: Stripe retries
			return
		}

		if !claimed {
			state, _, _ := cfg.Store.Get(ctx, key)
			if string(state) == stateDone {
				w.WriteHeader(http.StatusOK) // duplicate delivery
				return
			}

			http.Error(w, "event in progress", http.StatusConflict)

			return
		}

		if err := cfg.Bus.Dispatch(ctx, Received{Event: ev}); err != nil {
			_ = cfg.Store.Delete(context.WithoutCancel(ctx), key)
			cfg.Log.ErrorContext(ctx, "stripe webhook listener failed", "type", ev.Type, "id", ev.ID, "err", err)
			http.Error(w, "listener failed", http.StatusInternalServerError)

			return
		}

		_ = cfg.Store.Set(context.WithoutCancel(ctx), key, []byte(stateDone), 30*24*time.Hour)

		w.WriteHeader(http.StatusOK)
	}), nil
}

// SignForTest returns a Stripe-Signature header for payload, for tests
// that post webhooks to your app.
func SignForTest(payload []byte, secret string, at time.Time) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(payload)

	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}
