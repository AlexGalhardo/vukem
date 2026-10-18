package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// IdempotencyOptions tune Idempotency.
type IdempotencyOptions struct {
	// Methods guarded (default POST and PATCH).
	Methods []string
	// Required answers 400 when the header is missing.
	Required bool
	// TTL keeps completed responses (default 24h).
	TTL time.Duration
	// LockTTL bounds an in-flight request (default 1 minute).
	LockTTL time.Duration
	// MaxBody bounds the body of a keyed request (default 1 MiB). The body
	// is read whole to fingerprint it, so a larger one is refused with 413
	// before the handler runs, whatever a route-level body limit allows.
	// Raise it for keyed routes that accept bigger bodies; requests without
	// a key are not affected.
	MaxBody int64
	// ReplayHeaders lists the response headers stored and replayed besides
	// Content-Type. Default: the headers describing the stored
	// representation (Content-Language, Content-Disposition,
	// Content-Location, Location, ETag, Last-Modified, Link); a non-nil list
	// replaces it and Set-Cookie is never replayed. Every other header is
	// written fresh by the middleware of the repeat request (X-Request-ID,
	// RateLimit-*, security headers) or dropped, so a replay never carries
	// the first request's per-request values or its Set-Cookie.
	ReplayHeaders []string
	// Scope separates callers so one cannot replay another's response.
	// Default: the authenticated principal (auth.Principal: guard and user
	// id) when an auth middleware ran before Idempotency, else a SHA-256 of
	// the Authorization header. Place Idempotency after auth.Required so
	// a revoked token is refused before any replay. A request with a key
	// whose scope is empty is refused with 400; return a constant to share
	// keys on purpose.
	Scope func(r *http.Request) string
}

// IdempotencyHeader is the request header carrying the key.
const IdempotencyHeader = "Idempotency-Key"

type idemRecord struct {
	Fingerprint string      `json:"f"`
	Done        bool        `json:"d"`
	Status      int         `json:"s,omitempty"`
	Header      http.Header `json:"h,omitempty"`
	Body        []byte      `json:"b,omitempty"`
}

// Idempotency makes retried requests safe: the first request with an
// Idempotency-Key runs and its response (status below 500) is stored;
// repeats replay it with "Idempotent-Replayed: true". A repeat while the
// first is running gets 409, and reusing a key for a different request
// gets 422. If the store fails the request is refused with 503 (fail
// closed) rather than risking a duplicate side effect.
func Idempotency(store cache.Store, o IdempotencyOptions) func(http.Handler) http.Handler {
	if len(o.Methods) == 0 {
		o.Methods = []string{http.MethodPost, http.MethodPatch}
	}

	if o.TTL <= 0 {
		o.TTL = 24 * time.Hour
	}

	if o.LockTTL <= 0 {
		o.LockTTL = time.Minute
	}

	if o.MaxBody <= 0 {
		o.MaxBody = 1 << 20
	}

	if o.Scope == nil {
		o.Scope = defaultScope
	}

	replayed := []string{"Content-Type"}
	if o.ReplayHeaders == nil {
		o.ReplayHeaders = []string{
			"Content-Language", "Content-Disposition", "Content-Location", "Location", "ETag", "Last-Modified", "Link",
		}
	}

	for _, h := range o.ReplayHeaders {
		if h = http.CanonicalHeaderKey(h); h != "Set-Cookie" && !slices.Contains(replayed, h) {
			replayed = append(replayed, h)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(IdempotencyHeader)

			if !slices.Contains(o.Methods, r.Method) || (key == "" && !o.Required) {
				next.ServeHTTP(w, r)
				return
			}

			if !validKey(key) {
				web.Fail(
					w,
					r,
					errs.New(
						http.StatusBadRequest,
						"A valid Idempotency-Key header (1-255 printable characters) is required.",
					),
				)
				return
			}

			body, err := io.ReadAll(io.LimitReader(r.Body, o.MaxBody+1))
			if err != nil || int64(len(body)) > o.MaxBody {
				web.Fail(w, r, errs.ErrRequestTooLarge)
				return
			}

			// An empty scope would let every anonymous or cookie-session caller
			// replay each other's responses, so refuse instead of sharing it.
			scope := o.Scope(r)
			if scope == "" {
				web.SecurityEvent(r, "idempotency.unscoped")
				web.Fail(
					w,
					r,
					errs.New(http.StatusBadRequest, "Idempotency-Key requires an identified caller."),
				)
				return
			}

			r.Body = io.NopCloser(bytes.NewReader(body))

			ctx := r.Context()
			storeKey := "idempotency:" + digest(scope, "\x00", key)
			fingerprint := digest(r.Method, "\x00", r.URL.RequestURI(), "\x00", string(body))

			pending, _ := json.Marshal(idemRecord{Fingerprint: fingerprint})

			added, err := store.Add(ctx, storeKey, pending, o.LockTTL)
			if err != nil {
				web.Fail(w, r, errs.New(http.StatusServiceUnavailable, "Idempotency store unavailable."))
				return
			}

			if !added {
				replay(w, r, store, storeKey, fingerprint, replayed)
				return
			}

			rec := &recordingWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			if rec.status >= http.StatusInternalServerError {
				_ = store.Delete(ctx, storeKey) // let the client retry
				return
			}

			header := pick(rec.Header(), replayed)

			done, err := json.Marshal(idemRecord{
				Fingerprint: fingerprint, Done: true, Status: rec.status, Header: header, Body: rec.body.Bytes(),
			})
			if err == nil {
				err = store.Set(ctx, storeKey, done, o.TTL)
			}

			if log := web.ServicesFrom(ctx).Log; err != nil && log != nil {
				log.ErrorContext(ctx, "idempotency: store response", "err", err)
			}
		})
	}
}

// pick copies the listed headers of h.
func pick(h http.Header, names []string) http.Header {
	out := http.Header{}

	for _, n := range names {
		if v := h.Values(n); len(v) > 0 {
			out[n] = slices.Clone(v)
		}
	}

	return out
}

func replay(
	w http.ResponseWriter,
	r *http.Request,
	store cache.Store,
	storeKey, fingerprint string,
	replayed []string,
) {
	raw, ok, err := store.Get(r.Context(), storeKey)
	if err != nil {
		web.Fail(w, r, errs.New(http.StatusServiceUnavailable, "Idempotency store unavailable."))
		return
	}

	var rec idemRecord
	if !ok || json.Unmarshal(raw, &rec) != nil {
		web.Fail(w, r, errs.New(http.StatusConflict, "A request with this Idempotency-Key is being processed."))
		return
	}

	switch {
	case rec.Fingerprint != fingerprint:
		web.SecurityEvent(r, "idempotency.key_reused")
		web.Fail(
			w,
			r,
			errs.New(http.StatusUnprocessableEntity, "This Idempotency-Key was used for a different request."),
		)
	case !rec.Done:
		web.Fail(w, r, errs.New(http.StatusConflict, "A request with this Idempotency-Key is being processed."))
	default:
		// Records written before ReplayHeaders held every header: filter
		// again so they cannot overwrite this request's values either.
		maps.Copy(w.Header(), pick(rec.Header, replayed))

		w.Header().Set("Idempotent-Replayed", "true")
		w.WriteHeader(rec.Status)
		_, _ = w.Write(rec.Body)
	}
}

// defaultScope is the authenticated principal, else a hash of the
// Authorization header (never the credential itself), else "".
func defaultScope(r *http.Request) string {
	if guard, id, ok := auth.Principal(r.Context()); ok {
		// Length-prefixed, so ("a:b", "c") and ("a", "b:c") differ.
		return "principal:" + strconv.Itoa(len(guard)) + ":" + guard + ":" + id
	}

	if h := r.Header.Get("Authorization"); h != "" {
		return "authorization:" + digest(unprefixedToken(h))
	}

	return ""
}

// unprefixedToken drops the configurable prefix of an auth.Tokens bearer
// token ("Bearer vk_<id>.<secret>"), so one token keeps one scope whatever
// prefix it was issued under. The id and secret stay: a token id alone
// must never be enough to reach someone's stored responses. Other values
// are returned unchanged.
func unprefixedToken(h string) string {
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok {
		return h
	}

	id, secret, ok := strings.Cut(strings.TrimSpace(tok), ".")
	if i := strings.LastIndexByte(id, '_'); ok && i >= 0 {
		id = id[i+1:]
	}

	if !ok || !isRandText(id) || !isRandText(secret) {
		return h
	}

	return "Bearer " + id + "." + secret
}

// isRandText reports whether s looks like crypto/rand.Text output (26
// characters of base32), the form of auth.Tokens ids and secrets.
func isRandText(s string) bool {
	return len(s) == 26 && !strings.ContainsFunc(s, func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < '2' || r > '7')
	})
}

func validKey(k string) bool {
	if k == "" || len(k) > 255 {
		return false
	}

	for i := range len(k) {
		if k[i] < 0x21 || k[i] > 0x7e {
			return false
		}
	}

	return true
}

func digest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = io.WriteString(h, p)
	}

	return hex.EncodeToString(h.Sum(nil))
}

// recordingWriter writes through and keeps a copy of the response.
type recordingWriter struct {
	http.ResponseWriter

	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (w *recordingWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status, w.wroteHeader = code, true
	}

	w.ResponseWriter.WriteHeader(code)
}

func (w *recordingWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	w.body.Write(b)

	return w.ResponseWriter.Write(b)
}

func (w *recordingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
