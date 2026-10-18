package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

const defaultMaxBody = 1 << 20

// StandardOptions tune Standard.
type StandardOptions struct {
	// MaxBody bounds the body (default 1 MiB).
	MaxBody int64
	// Tolerance is how far webhook-timestamp may be from now, in either
	// direction (default 5 minutes). It bounds replays of captured requests.
	Tolerance time.Duration
	// Now is the clock (default time.Now).
	Now func() time.Time
}

// Standard verifies the Standard Webhooks scheme (https://www.standardwebhooks.com,
// used by Svix, Resend, Clerk and others): webhook-signature carries one or
// more "v1,<base64 HMAC-SHA256>" over "<webhook-id>.<webhook-timestamp>.<body>",
// and webhook-timestamp must be within Tolerance. secret is the
// provider's "whsec_<base64>" value (the prefix is optional). A malformed
// secret is an error at boot rather than a webhook that never verifies.
func Standard(secret string, o StandardOptions) (func(http.Handler) http.Handler, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil || len(key) == 0 {
		return nil, errors.New("webhook: Standard secret must be base64, optionally prefixed with whsec_")
	}

	if o.MaxBody <= 0 {
		o.MaxBody = defaultMaxBody
	}

	if o.Tolerance <= 0 {
		o.Tolerance = 5 * time.Minute
	}

	if o.Now == nil {
		o.Now = time.Now
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(io.LimitReader(r.Body, o.MaxBody+1))
			if err != nil || int64(len(body)) > o.MaxBody {
				web.Fail(w, r, errs.ErrRequestTooLarge)
				return
			}

			if !verifyStandard(key, r.Header, body, o.Now(), o.Tolerance) {
				web.SecurityEvent(r, "webhook.signature_invalid", "header", "webhook-signature")
				web.Fail(w, r, ErrSignature)

				return
			}

			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}, nil
}

func verifyStandard(key []byte, h http.Header, body []byte, now time.Time, tolerance time.Duration) bool {
	id, rawTS := h.Get("webhook-id"), h.Get("webhook-timestamp")
	if id == "" {
		return false
	}

	ts, err := strconv.ParseInt(rawTS, 10, 64)
	if err != nil || ts <= 0 {
		return false
	}

	if d := now.Sub(time.Unix(ts, 0)); d > tolerance || d < -tolerance {
		return false
	}

	m := hmac.New(sha256.New, key)
	m.Write([]byte(id + "." + rawTS + "."))
	m.Write(body)
	want := m.Sum(nil)

	// Providers send several signatures during secret rotation.
	for sig := range strings.FieldsSeq(h.Get("webhook-signature")) {
		v, b64, ok := strings.Cut(sig, ",")
		if !ok || v != "v1" {
			continue
		}

		if got, err := base64.StdEncoding.DecodeString(b64); err == nil && hmac.Equal(got, want) {
			return true
		}
	}

	return false
}

// StandardID returns the webhook-id header, the event id for Dedupe with
// Standard Webhooks providers.
func StandardID(r *http.Request) string { return r.Header.Get("webhook-id") }

// DedupeOptions tune Dedupe.
type DedupeOptions struct {
	// TTL remembers handled event ids (default 72h, longer than providers'
	// retry windows).
	TTL time.Duration
	// LockTTL bounds a delivery that is still running (default 1 minute).
	LockTTL time.Duration
	// Namespace keeps event ids of different providers or handlers apart
	// on a shared store (default: the matched ServeMux route pattern, or
	// the request path when Dedupe runs outside a route). Set it when one
	// handler serves several providers, or to keep keys stable across a
	// route rename.
	Namespace string
}

// Dedupe runs the handler at most once per event id: providers deliver
// at least once, so a redelivery of a handled event gets 200 with
// "Webhook-Duplicate: true" without running the handler again, and a
// duplicate that arrives while the first is running gets 409 (the
// provider retries). Deliveries answered with a status of 300 or above are
// forgotten so the retry runs. A missing id is a 400, and a store failure
// is a 503 (fail closed). Keys are namespaced (DedupeOptions.Namespace),
// so two providers that reuse an event id do not drop each other's
// deliveries. Put it after the signature check.
func Dedupe(store cache.Store, id func(*http.Request) string, o DedupeOptions) func(http.Handler) http.Handler {
	if o.TTL <= 0 {
		o.TTL = 72 * time.Hour
	}

	if o.LockTTL <= 0 {
		o.LockTTL = time.Minute
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			eventID := id(r)
			if eventID == "" {
				web.Fail(w, r, errs.New(http.StatusBadRequest, "Missing webhook event id."))
				return
			}

			ctx := r.Context()
			key := dedupeKey(namespace(r, o.Namespace), eventID)

			added, err := store.Add(ctx, key, []byte("running"), o.LockTTL)
			if err != nil {
				web.Fail(w, r, errs.New(http.StatusServiceUnavailable, "Webhook store unavailable."))
				return
			}

			if !added {
				state, _, err := store.Get(ctx, key)

				switch {
				case err != nil:
					web.Fail(w, r, errs.New(http.StatusServiceUnavailable, "Webhook store unavailable."))
				case string(state) == "done":
					w.Header().Set("Webhook-Duplicate", "true")
					w.WriteHeader(http.StatusOK)
				default:
					web.Fail(w, r, errs.New(http.StatusConflict, "This webhook event is being processed."))
				}

				return
			}

			rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			if rec.status >= http.StatusMultipleChoices {
				_ = store.Delete(ctx, key)
				return
			}

			if err := store.Set(ctx, key, []byte("done"), o.TTL); err != nil {
				if log := web.ServicesFrom(ctx).Log; log != nil {
					log.ErrorContext(ctx, "webhook: remember event", "id", eventID, "err", err)
				}
			}
		})
	}
}

func namespace(r *http.Request, fixed string) string {
	switch {
	case fixed != "":
		return fixed
	case r.Pattern != "":
		return r.Pattern
	default:
		return r.URL.Path
	}
}

// dedupeKey length-prefixes the namespace, so a ":" in it or in the event
// id can never make two pairs share a key.
func dedupeKey(ns, eventID string) string {
	return "webhook:event:" + strconv.Itoa(len(ns)) + ":" + ns + ":" + eventID
}

type statusWriter struct {
	http.ResponseWriter

	status int
	wrote  bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wrote {
		s.status, s.wrote = code, true
	}

	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
