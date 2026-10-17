package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// SessionGuard authenticates browsers with the session: the user id is
// stored under "auth_<name>" after login.
type SessionGuard[U Authenticatable] struct {
	name     string
	provider UserProvider[U]
	hasher   hash.Hasher
	remember *RememberStore
	// IdentifierField is where failed-login errors are reported ("email").
	IdentifierField string
	// RememberFor is the remember-me cookie lifetime (default 30 days).
	RememberFor time.Duration
	// InsecureCookies drops the Secure flag on the remember-me cookie
	// (plain-HTTP development without localhost only).
	InsecureCookies bool

	dummy string // hash verified when the user does not exist (timing)
}

// NewSessionGuard returns the "web" style guard. remember may be nil to
// disable remember-me.
func NewSessionGuard[U Authenticatable](name string, p UserProvider[U], h hash.Hasher,
	remember *RememberStore,
) *SessionGuard[U] {
	dummy, _ := h.Make("dummy password for timing equalisation")

	return &SessionGuard[U]{
		name: name, provider: p, hasher: h, remember: remember,
		IdentifierField: "email", RememberFor: 30 * 24 * time.Hour, dummy: dummy,
	}
}

// Name implements Checker.
func (g *SessionGuard[U]) Name() string { return g.name }

func (g *SessionGuard[U]) key() string { return "auth_" + g.name }

// LoginOption tunes Attempt and Login.
type LoginOption func(*loginOpts)

type loginOpts struct{ remember bool }

// Remember issues a remember-me cookie on login.
func Remember(on bool) LoginOption { return func(o *loginOpts) { o.remember = on } }

// Attempt checks creds (identifier columns + "password") and logs the user
// in. It returns a 422 validation error on the identifier field when they
// do not match, taking the same time whether or not the user exists.
func (g *SessionGuard[U]) Attempt(c *web.Ctx, creds map[string]string, opts ...LoginOption) (U, error) {
	u, err := g.Validate(c, creds)
	if err != nil {
		return u, err
	}

	return u, g.Login(c, u, opts...)
}

// Validate checks creds like Attempt without logging in (API token
// issuance). Unknown users cost the same hash verification and get the
// same 422 error; a stale hash is upgraded after a successful check.
func (g *SessionGuard[U]) Validate(c *web.Ctx, creds map[string]string) (U, error) {
	var zero U

	u, err := g.provider.ByCredentials(c.Context(), creds)

	switch {
	case errors.Is(err, ErrUserNotFound):
		_, _ = g.hasher.Verify(g.dummy, creds["password"])
		web.SecurityEvent(c.Request(), "auth.login_failed", "guard", g.name)

		return zero, credentialsError(g.IdentifierField)
	case err != nil:
		return zero, err
	}

	ok, err := g.hasher.Verify(u.AuthPassword(), creds["password"])
	if err != nil || !ok {
		web.SecurityEvent(c.Request(), "auth.login_failed", "guard", g.name)
		return zero, credentialsError(g.IdentifierField)
	}

	if g.hasher.NeedsRehash(u.AuthPassword()) {
		if up, ok := g.provider.(PasswordUpdater[U]); ok {
			if h, err := g.hasher.Make(creds["password"]); err == nil {
				_ = up.UpdatePassword(c.Context(), u, h) // best effort: the credentials were valid
			}
		}
	}

	return u, nil
}

// Login logs u in: the session id is regenerated (fixation) and the user
// id stored, also under web.SessionUserKey so server-side session stores
// can revoke the user's other sessions (session.Manager.RevokeOthers).
func (g *SessionGuard[U]) Login(c *web.Ctx, u U, opts ...LoginOption) error {
	var o loginOpts
	for _, opt := range opts {
		opt(&o)
	}

	s := c.Session()
	if s == nil {
		return errors.New("auth: session guard needs the session middleware")
	}

	s.Regenerate()
	s.Put(g.key(), u.AuthID())
	s.Put(web.SessionUserKey, g.SessionUserID(u.AuthID()))
	cache(c, g.name, u)

	if o.remember && g.remember != nil {
		return g.issueRemember(c, u.AuthID())
	}

	return nil
}

// SessionUserID is the owner recorded with server-side sessions for the
// user id (web.SessionUserKey): "<guard>:<id>", so guards over different
// user tables never share an owner. Pass it to session.Manager.RevokeOthers.
func (g *SessionGuard[U]) SessionUserID(id string) string { return g.name + ":" + id }

// backfillSessionUser records the owner of sessions logged in before it
// existed (v0.23 and earlier), or whose recorded guard has since logged
// out. While another guard is still logged in, its owner is kept: the last
// login owns the session.
func (g *SessionGuard[U]) backfillSessionUser(s web.Session, id string) {
	want := g.SessionUserID(id)

	cur, _ := s.Get(web.SessionUserKey).(string)
	if cur == want {
		return
	}

	if guard, uid, ok := strings.Cut(cur, ":"); ok {
		if other, _ := s.Get("auth_" + guard).(string); other == uid {
			return
		}
	}

	s.Put(web.SessionUserKey, want)
}

// Logout clears the session (new id, no data) and the remember-me token.
func (g *SessionGuard[U]) Logout(c *web.Ctx) error {
	if s := c.Session(); s != nil {
		s.Invalidate()
	}

	if g.remember != nil {
		if id, _, ok := strings.Cut(c.Cookie(g.cookieName()), "."); ok {
			_ = g.remember.delete(c.Context(), id)
		}

		g.clearCookie(c)
	}

	cache(c, g.name, nil)

	return nil
}

// isNotFound reports a provider's "no such user": ErrUserNotFound, or the
// orm.ErrNotFound / sql.ErrNoRows custom providers often return wrapped.
func isNotFound(err error) bool {
	return errors.Is(err, ErrUserNotFound) || errors.Is(err, orm.ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}

// authenticate is Check that tells a guest (ErrUnauthenticated) apart
// from a failed lookup, which Required answers with its error.
func (g *SessionGuard[U]) authenticate(c *web.Ctx) error {
	_, err := g.User(c)
	return err
}

// Check implements Checker: session user, else a valid remember-me cookie.
func (g *SessionGuard[U]) Check(c *web.Ctx) bool {
	_, err := g.User(c)
	return err == nil
}

// User returns the logged-in user (cached for the request) or
// ErrUnauthenticated.
func (g *SessionGuard[U]) User(c *web.Ctx) (U, error) {
	if u, ok := cached[U](c, g.name); ok {
		return u, nil
	}

	var zero U

	s := c.Session()
	if s == nil {
		return zero, ErrUnauthenticated
	}

	if id, ok := s.Get(g.key()).(string); ok && id != "" {
		u, err := g.provider.ByID(c.Context(), id)

		switch {
		case err == nil:
			g.backfillSessionUser(s, id)

			cache(c, g.name, u)

			return u, nil
		case isNotFound(err):
			s.Forget(g.key()) // the user was deleted
		default:
			// The store is failing: refuse the request but keep the session,
			// so an outage does not log everyone out.
			return zero, errs.Wrap(http.StatusServiceUnavailable, fmt.Errorf("auth: load user: %w", err))
		}
	}

	if g.remember != nil {
		if u, ok := g.fromRemember(c); ok {
			return u, nil
		}
	}

	return zero, ErrUnauthenticated
}

func (g *SessionGuard[U]) cookieName() string { return "remember_" + g.name }

func (g *SessionGuard[U]) issueRemember(c *web.Ctx, userID string) error {
	value, err := g.remember.create(c.Context(), userID, time.Now().Add(g.RememberFor))
	if err != nil {
		return err
	}

	c.SetCookie(g.cookie(value, int(g.RememberFor.Seconds())))

	return nil
}

func (g *SessionGuard[U]) clearCookie(c *web.Ctx) { c.SetCookie(g.cookie("", -1)) }

// cookie is HttpOnly and SameSite=Lax; Secure unless InsecureCookies.
func (g *SessionGuard[U]) cookie(value string, maxAge int) *http.Cookie {
	// G124: Secure is on unless the app opts out with InsecureCookies.
	return &http.Cookie{ //nolint:gosec // see above
		Name: g.cookieName(), Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: !g.InsecureCookies, SameSite: http.SameSiteLaxMode,
	}
}

// fromRemember logs the user in from the remember-me cookie, rotating the
// token so a stolen cookie works at most once.
func (g *SessionGuard[U]) fromRemember(c *web.Ctx) (U, bool) {
	var zero U

	raw := c.Cookie(g.cookieName())
	if raw == "" {
		return zero, false
	}

	userID, err := g.remember.consume(c.Context(), raw)
	if err != nil {
		g.clearCookie(c)
		web.SecurityEvent(c.Request(), "auth.remember_rejected", "guard", g.name)

		return zero, false
	}

	u, err := g.provider.ByID(c.Context(), userID)
	if err != nil {
		g.clearCookie(c)
		return zero, false
	}

	if err := g.Login(c, u, Remember(true)); err != nil {
		return zero, false
	}

	return u, true
}

// RememberStore keeps remember-me tokens: the cookie holds "<id>.<secret>"
// and only SHA-256(secret) is stored. Table (created by install:auth):
// remember_tokens(id TEXT PRIMARY KEY, user_id TEXT, hash TEXT, expires_at TIMESTAMP).
type RememberStore struct {
	ex    database.Executor
	table string
	now   func() time.Time
}

// NewRememberStore uses the remember_tokens table.
func NewRememberStore(ex database.Executor) *RememberStore {
	return &RememberStore{ex: ex, table: "remember_tokens", now: time.Now}
}

func (r *RememberStore) q(sqlText string) string {
	d := r.ex.Dialect()

	n := 0

	var b strings.Builder

	for _, ch := range sqlText {
		if ch == '?' {
			n++
			b.WriteString(d.Placeholder(n))

			continue
		}

		b.WriteRune(ch)
	}

	return strings.ReplaceAll(b.String(), "{table}", d.Quote(r.table))
}

func (r *RememberStore) create(ctx context.Context, userID string, expires time.Time) (string, error) {
	id := rand.Text()
	secret := rand.Text()

	if _, err := r.ex.ExecContext(ctx, r.q("INSERT INTO {table} (id, user_id, hash, expires_at) VALUES (?, ?, ?, ?)"),
		id, userID, digest(secret), expires.UTC()); err != nil {
		return "", err
	}

	return id + "." + secret, nil
}

var errBadToken = errors.New("auth: invalid token")

// consume validates and deletes the token, returning its user id.
func (r *RememberStore) consume(ctx context.Context, raw string) (string, error) {
	id, secret, ok := strings.Cut(raw, ".")
	if !ok || id == "" || secret == "" {
		return "", errBadToken
	}

	var (
		userID, stored string
		expires        time.Time
	)

	err := r.ex.QueryRowContext(ctx, r.q("SELECT user_id, hash, expires_at FROM {table} WHERE id = ?"), id).
		Scan(&userID, &stored, &expires)
	if err != nil {
		return "", errBadToken
	}

	// Single use, valid or not. Only the request whose DELETE removed the
	// row may log in: two concurrent requests with a stolen cookie both
	// pass the SELECT, and a failed DELETE would leave the token reusable.
	res, err := r.ex.ExecContext(ctx, r.q("DELETE FROM {table} WHERE id = ?"), id)
	if err != nil {
		return "", errBadToken
	}

	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return "", errBadToken
	}

	if subtle.ConstantTimeCompare([]byte(stored), []byte(digest(secret))) != 1 || !expires.After(r.now()) {
		return "", errBadToken
	}

	return userID, nil
}

func (r *RememberStore) delete(ctx context.Context, id string) error {
	_, err := r.ex.ExecContext(ctx, r.q("DELETE FROM {table} WHERE id = ?"), id)
	return err
}

func digest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
