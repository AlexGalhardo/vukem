package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
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
// disable remember-me. It panics when *U implements SessionVersioner but U
// does not (AuthSessionVersion on a pointer receiver while the guard holds
// values): the version would never be checked, so sessions could not be
// ended.
func NewSessionGuard[U Authenticatable](name string, p UserProvider[U], h hash.Hasher,
	remember *RememberStore,
) *SessionGuard[U] {
	var u U
	if _, onValue := any(u).(SessionVersioner); !onValue {
		if _, onPointer := any(&u).(SessionVersioner); onPointer {
			panic(fmt.Sprintf("auth: %T implements SessionVersioner with a pointer receiver; "+
				"declare AuthSessionVersion on the value receiver (func (u %T) AuthSessionVersion() int64)", &u, u))
		}
	}

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

		return zero, credentialsError(c.Context(), g.IdentifierField)
	case err != nil:
		return zero, err
	}

	ok, err := g.hasher.Verify(u.AuthPassword(), creds["password"])
	if err != nil || !ok {
		web.SecurityEvent(c.Request(), "auth.login_failed", "guard", g.name)
		return zero, credentialsError(c.Context(), g.IdentifierField)
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

	for k, v := range SessionValues(g.name, u) {
		s.Put(k, v)
	}

	cache(c, g.name, u)

	if o.remember && g.remember != nil {
		return g.issueRemember(c, u)
	}

	return nil
}

// SessionVersioner is implemented by users whose browser sessions can all
// be ended at once by changing a number kept with the user (a
// session_version column): SessionGuard records it on login and signs the
// session out when it no longer matches, on every session driver,
// encrypted cookies included. Bump it on a password reset or change; it
// costs nothing per request, since the guard loads the user anyway.
type SessionVersioner interface {
	AuthSessionVersion() int64
}

// SessionValues returns the session values SessionGuard.Login stores for
// user on the guard named guard: the user id, the owner under
// web.SessionUserKey and, for a SessionVersioner, its session version.
// Tests use it to start a logged-in session (vukemtest Client.ActingAs).
func SessionValues(guard string, user interface{ AuthID() string }) map[string]any {
	values := map[string]any{
		"auth_" + guard:    user.AuthID(),
		web.SessionUserKey: guard + ":" + user.AuthID(),
	}

	if v, ok := user.(SessionVersioner); ok {
		values[versionKey(guard)] = v.AuthSessionVersion()
	}

	return values
}

func versionKey(guard string) string { return "_auth_version_" + guard }

// sessionVersionOK reports whether the version recorded in s matches u's.
// Sessions logged in before versions existed carry none, which counts as
// 0: they last until the first bump. Anything unreadable is stale.
func (g *SessionGuard[U]) sessionVersionOK(s web.Session, u U) bool {
	v, ok := any(u).(SessionVersioner)
	if !ok {
		return true
	}

	var recorded int64

	switch x := s.Get(versionKey(g.name)).(type) {
	case nil:
	case int64:
		recorded = x
	case int:
		recorded = int64(x)
	case json.Number:
		n, err := strconv.ParseInt(string(x), 10, 64)
		if err != nil {
			return false
		}

		recorded = n
	default:
		return false
	}

	return recorded == v.AuthSessionVersion()
}

// RefreshSession reloads the logged-in user and records its current
// session version in this session, and issues a new remember-me token
// when the request carries one: call it after bumping the version when the
// current session must stay signed in (two-factor authentication turned
// on, password changed: the others end).
func (g *SessionGuard[U]) RefreshSession(c *web.Ctx) error {
	s := c.Session()
	if s == nil {
		return errors.New("auth: session guard needs the session middleware")
	}

	id, _ := s.Get(g.key()).(string)
	if id == "" {
		return ErrUnauthenticated
	}

	u, err := g.provider.ByID(c.Context(), id)
	if err != nil {
		return fmt.Errorf("auth: refresh session: %w", err)
	}

	if v, ok := any(u).(SessionVersioner); ok {
		s.Put(versionKey(g.name), v.AuthSessionVersion())
	}

	cache(c, g.name, u)

	// Remember-me tokens are bound to the version they were issued under:
	// the current device gets one for the new version.
	if g.remember != nil {
		if raw := c.Cookie(g.cookieName()); raw != "" {
			if tokenID, _, ok := strings.Cut(raw, "."); ok {
				_ = g.remember.delete(c.Context(), tokenID, u.AuthID())
			}

			return g.issueRemember(c, u)
		}
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
	userID := ""

	if s := c.Session(); s != nil {
		userID, _ = s.Get(g.key()).(string)
		s.Invalidate()
	}

	if g.remember != nil {
		// Only the signed-out user's own token: a cookie planted from
		// another account must not delete that account's token.
		if tokenID, _, ok := strings.Cut(c.Cookie(g.cookieName()), "."); ok && userID != "" {
			_ = g.remember.delete(c.Context(), tokenID, userID)
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
		case err == nil && !g.sessionVersionOK(s, u):
			// The user's sessions were ended (password reset) after this
			// login: end this one whole, so nothing stays attributed to the
			// user (owner, other guards' keys, data).
			s.Invalidate()
			web.SecurityEvent(c.Request(), "auth.session_revoked", "guard", g.name)
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

func (g *SessionGuard[U]) issueRemember(c *web.Ctx, u U) error {
	value, err := g.remember.create(c.Context(), u.AuthID(), sessionVersion(u), time.Now().Add(g.RememberFor))
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

	tok, err := g.remember.consume(c.Context(), raw)
	if err != nil {
		g.clearCookie(c)
		web.SecurityEvent(c.Request(), "auth.remember_rejected", "guard", g.name)

		return zero, false
	}

	u, err := g.provider.ByID(c.Context(), tok.userID)
	if err != nil {
		g.clearCookie(c)
		return zero, false
	}

	// Checked once the user is known: a token issued before the user's
	// sessions were ended (session version bumped) is rejected.
	if !tok.matches(sessionVersion(u)) {
		g.clearCookie(c)
		web.SecurityEvent(c.Request(), "auth.remember_rejected", "guard", g.name, "reason", "session version")

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

func (r *RememberStore) create(ctx context.Context, userID string, version int64, expires time.Time) (string, error) {
	id := rand.Text()
	secret := rand.Text()

	if _, err := r.ex.ExecContext(ctx, r.q("INSERT INTO {table} (id, user_id, hash, expires_at) VALUES (?, ?, ?, ?)"),
		id, userID, rememberHash(secret, version), expires.UTC()); err != nil {
		return "", err
	}

	return id + "." + secret, nil
}

var errBadToken = errors.New("auth: invalid token")

// rememberHash is the stored hash of a remember-me secret issued under a
// session version. Version 0 hashes the secret alone, so tokens issued
// before versions existed keep working until the first bump.
func rememberHash(secret string, version int64) string {
	if version == 0 {
		return digest(secret)
	}

	return digest(secret + "|v" + strconv.FormatInt(version, 10))
}

// consumedToken is a remember-me token deleted by consume; matches checks
// it against the user's current session version.
type consumedToken struct {
	userID, stored, secret string
}

func (t consumedToken) matches(version int64) bool {
	return subtle.ConstantTimeCompare([]byte(t.stored), []byte(rememberHash(t.secret, version))) == 1
}

func sessionVersion(u any) int64 {
	if v, ok := u.(SessionVersioner); ok {
		return v.AuthSessionVersion()
	}

	return 0
}

// consume deletes the token and checks its expiry; the caller checks the
// secret with matches once the user (and so the session version) is known.
func (r *RememberStore) consume(ctx context.Context, raw string) (consumedToken, error) {
	id, secret, ok := strings.Cut(raw, ".")
	if !ok || id == "" || secret == "" {
		return consumedToken{}, errBadToken
	}

	var (
		userID, stored string
		expires        time.Time
	)

	err := r.ex.QueryRowContext(ctx, r.q("SELECT user_id, hash, expires_at FROM {table} WHERE id = ?"), id).
		Scan(&userID, &stored, &expires)
	if err != nil {
		return consumedToken{}, errBadToken
	}

	// Single use, valid or not. Only the request whose DELETE removed the
	// row may log in: two concurrent requests with a stolen cookie both
	// pass the SELECT, and a failed DELETE would leave the token reusable.
	res, err := r.ex.ExecContext(ctx, r.q("DELETE FROM {table} WHERE id = ?"), id)
	if err != nil {
		return consumedToken{}, errBadToken
	}

	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return consumedToken{}, errBadToken
	}

	if !expires.After(r.now()) {
		return consumedToken{}, errBadToken
	}

	return consumedToken{userID: userID, stored: stored, secret: secret}, nil
}

// delete removes token id when it belongs to userID.
func (r *RememberStore) delete(ctx context.Context, id, userID string) error {
	_, err := r.ex.ExecContext(ctx, r.q("DELETE FROM {table} WHERE id = ? AND user_id = ?"), id, userID)
	return err
}

func digest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
