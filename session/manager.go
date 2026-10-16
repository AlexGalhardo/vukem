package session

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/AlexGalhardo/golang-framework/errs"
	"github.com/AlexGalhardo/golang-framework/web"
)

// Config configures a Manager. Zero values use secure defaults.
type Config struct {
	// CookieName defaults to "lumek_session"; with Secure, Path "/" and no
	// Domain the "__Host-" prefix is added automatically.
	CookieName string
	// Path defaults to "/".
	Path string
	// Domain is empty (host-only cookie) by default.
	Domain string
	// Secure sends the cookie over HTTPS only. Required in production.
	Secure bool
	// SameSite defaults to Lax.
	SameSite http.SameSite
	// IdleTimeout ends sessions after inactivity (default 2h).
	IdleTimeout time.Duration
	// Lifetime ends sessions this long after creation (default 24h).
	Lifetime time.Duration
	// Store keeps sessions server-side; nil stores them in an encrypted
	// cookie (CookieKey required).
	Store Store
	// CookieKey is the 32-byte AES-256-GCM key for cookie sessions; derive
	// it from APP_KEY: app.Key.Derive("session.cookie", 32).
	CookieKey []byte
	// Production enables boot-time checks (Secure must be true).
	Production bool
	// Now is the clock (tests); defaults to time.Now.
	Now func() time.Time
	// Log receives security events and storage errors.
	Log *slog.Logger
}

// Manager loads and saves sessions around each request.
type Manager struct {
	cfg    Config
	sealer *sealer
	name   string
}

// New validates cfg and returns a Manager. Insecure production settings are
// boot errors (docs/security.md, A02/A07).
func New(cfg Config) (*Manager, error) {
	if cfg.Path == "" {
		cfg.Path = "/"
	}

	if cfg.SameSite == 0 {
		cfg.SameSite = http.SameSiteLaxMode
	}

	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 2 * time.Hour
	}

	if cfg.Lifetime <= 0 {
		cfg.Lifetime = 24 * time.Hour
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}

	if cfg.Production && !cfg.Secure {
		return nil, errors.New("session: cookies must be Secure in production (serve over HTTPS)")
	}

	if cfg.SameSite == http.SameSiteNoneMode && !cfg.Secure {
		return nil, errors.New("session: SameSite=None requires Secure")
	}

	m := &Manager{cfg: cfg}

	name := cfg.CookieName
	if name == "" {
		name = "lumek_session"
	}

	if cfg.Secure && cfg.Path == "/" && cfg.Domain == "" && !strings.HasPrefix(name, "__Host-") {
		name = "__Host-" + name
	}

	m.name = name

	if cfg.Store == nil {
		if len(cfg.CookieKey) != 32 {
			return nil, errors.New("session: cookie sessions need a 32-byte CookieKey (derive it from APP_KEY)")
		}

		s, err := newSealer(cfg.CookieKey)
		if err != nil {
			return nil, err
		}

		m.sealer = s
	}

	return m, nil
}

// CookieName returns the effective cookie name.
func (m *Manager) CookieName() string { return m.name }

var idRe = regexp.MustCompile(`^[A-Z2-7]{26}$`)

func newID() string { return rand.Text() }

// Middleware loads the session lazily, exposes it through web.Ctx (and
// web.SessionFrom), installs the flash-and-redirect validation hook, and
// saves it before the response headers are sent.
func (m *Manager) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess := &Session{}
			sess.load = func(s *Session) { m.load(r, s) }

			ctx := web.WithSession(r.Context(), sess)
			ctx = web.WithValidationHook(ctx, validationHook)
			r = r.WithContext(ctx)

			cw := &commitWriter{ResponseWriter: w}
			cw.commit = func() { m.commit(cw, r, sess) }

			next.ServeHTTP(cw, r)
			cw.ensureCommitted()
		})
	}
}

// load reads the session from the cookie (and store). Tampered or expired
// sessions are replaced by a fresh one.
func (m *Manager) load(r *http.Request, s *Session) {
	s.data = map[string]any{}

	ck, err := r.Cookie(m.name)
	if err != nil || ck.Value == "" {
		return
	}

	var raw []byte

	if m.sealer != nil {
		raw, err = m.sealer.open(m.name, ck.Value)
		if err != nil {
			web.SecurityEvent(r, "session.invalid", "reason", "cookie authentication failed")
			return
		}
	} else {
		if !idRe.MatchString(ck.Value) {
			web.SecurityEvent(r, "session.invalid", "reason", "malformed session id")
			return
		}

		var found bool

		raw, found, err = m.cfg.Store.Load(r.Context(), ck.Value)
		if err != nil {
			m.cfg.Log.ErrorContext(r.Context(), "session load failed", "err", err)
			return
		}

		if !found {
			return
		}

		s.id = ck.Value
	}

	data, err := decode(raw)
	if err != nil {
		web.SecurityEvent(r, "session.invalid", "reason", "undecodable data")
		return
	}

	now := m.cfg.Now()

	created, okC := unixOf(data[keyCreated])
	last, okL := unixOf(data[keyLast])

	if !okC || !okL || now.Sub(last) > m.cfg.IdleTimeout || now.Sub(created) > m.cfg.Lifetime {
		// Expired: start over, and drop the stale server-side record.
		s.oldID = s.id
		s.id = ""
		s.dirty = s.oldID != ""

		return
	}

	s.data = data
}

// Peek decodes the session carried by cookies, as the next request would
// see it. Tests use it to assert on flash data and session values.
func (m *Manager) Peek(ctx context.Context, cookies []*http.Cookie) map[string]any {
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)
	for _, c := range cookies {
		r.AddCookie(c)
	}

	s := &Session{}
	m.load(r, s)

	return s.data
}

// touchEvery limits idle-timeout refresh writes to one per minute.
const touchEvery = time.Minute

func (m *Manager) commit(w http.ResponseWriter, r *http.Request, s *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.loaded {
		return // never used during this request
	}

	now := m.cfg.Now()
	last, hasLast := unixOf(s.data[keyLast])

	needsTouch := !hasLast || now.Sub(last) >= touchEvery
	if !s.dirty && !s.hasFlash() && !needsTouch {
		return
	}

	ctx := context.WithoutCancel(r.Context())

	if s.oldID != "" && m.cfg.Store != nil {
		if err := m.cfg.Store.Delete(ctx, s.oldID); err != nil {
			m.cfg.Log.ErrorContext(ctx, "session delete failed", "err", err)
		}
	}

	s.ageFlash()

	if s.destroyed && len(userKeys(s.data)) == 0 {
		m.clearCookie(w)
		return
	}

	if _, ok := s.data[keyCreated]; !ok {
		s.data[keyCreated] = now.Unix()
	}

	s.data[keyLast] = now.Unix()

	created, _ := unixOf(s.data[keyCreated])
	expiry := now.Add(m.cfg.IdleTimeout)
	if absolute := created.Add(m.cfg.Lifetime); absolute.Before(expiry) {
		expiry = absolute
	}

	raw, err := encode(s.data)
	if err != nil {
		m.cfg.Log.ErrorContext(ctx, "session encode failed", "err", err)
		return
	}

	value := ""

	if m.sealer != nil {
		value = m.sealer.seal(m.name, raw)
		if len(value) > maxCookieSize {
			m.cfg.Log.ErrorContext(ctx, "session not saved", "err", ErrCookieTooLarge)
			return
		}
	} else {
		if s.id == "" {
			s.id = newID()
		}

		if err := m.cfg.Store.Save(ctx, s.id, raw, expiry); err != nil {
			m.cfg.Log.ErrorContext(ctx, "session save failed", "err", err)
			return
		}

		value = s.id
	}

	//nolint:gosec // G124: Secure follows config (forced in production by New); HttpOnly and SameSite always set
	http.SetCookie(w, &http.Cookie{
		Name:     m.name,
		Value:    value,
		Path:     m.cfg.Path,
		Domain:   m.cfg.Domain,
		Expires:  expiry,
		MaxAge:   int(expiry.Sub(now).Seconds()),
		Secure:   m.cfg.Secure,
		HttpOnly: true,
		SameSite: m.cfg.SameSite,
	})
}

func (m *Manager) clearCookie(w http.ResponseWriter) {
	//nolint:gosec // G124: deletion cookie mirrors the session cookie attributes
	http.SetCookie(w, &http.Cookie{
		Name: m.name, Value: "", Path: m.cfg.Path, Domain: m.cfg.Domain, MaxAge: -1,
		Secure: m.cfg.Secure, HttpOnly: true, SameSite: m.cfg.SameSite,
	})
}

func userKeys(data map[string]any) []string {
	var keys []string

	for k := range data {
		if !strings.HasPrefix(k, internalPref) {
			keys = append(keys, k)
		}
	}

	return keys
}

// validationHook flashes field errors and old input, then redirects back
// (Laravel-style form validation). Password fields are never flashed.
func validationHook(c *web.Ctx, verr *errs.ValidationError) bool {
	s := c.Session()
	if s == nil {
		return false
	}

	s.Flash(web.ErrorsKey, verr.Fields)

	if r := c.Request(); r.PostForm != nil {
		old := web.FormToMap(r.PostForm)
		for k := range old {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "password") || k == "_token" || k == "_method" {
				delete(old, k)
			}
		}

		s.Flash(web.OldInputKey, old)
	}

	_ = c.Back("/")

	return true
}

// commitWriter saves the session right before the first byte or header of
// the response is written, while Set-Cookie can still be added.
type commitWriter struct {
	http.ResponseWriter

	commit    func()
	committed bool
}

func (w *commitWriter) ensureCommitted() {
	if !w.committed {
		w.committed = true
		w.commit()
	}
}

func (w *commitWriter) WriteHeader(code int) {
	w.ensureCommitted()
	w.ResponseWriter.WriteHeader(code)
}

func (w *commitWriter) Write(b []byte) (int, error) {
	w.ensureCommitted()
	return w.ResponseWriter.Write(b)
}

// Flush supports streaming responses.
func (w *commitWriter) Flush() {
	w.ensureCommitted()

	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap supports http.ResponseController.
func (w *commitWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
