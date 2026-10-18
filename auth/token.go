package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/web"
)

// Token describes an access token (never the secret).
type Token struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	Abilities  []string   `json:"abilities"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	// RevokedAt is when the token was revoked; a revoked token is kept for
	// the audit trail (List shows it) until Prune deletes it.
	RevokedAt *time.Time `json:"revoked_at"`
}

// Can reports whether the token grants ability ("*" grants everything).
func (t Token) Can(ability string) bool {
	return slices.Contains(t.Abilities, "*") || slices.Contains(t.Abilities, ability)
}

// Tokens issues and checks opaque access tokens "<prefix><id>.<secret>";
// only SHA-256(secret) is stored. Table (created by install:auth / install:api):
// access_tokens(id TEXT PRIMARY KEY, user_id TEXT, name TEXT, hash TEXT,
// abilities TEXT, last_used_at TIMESTAMP NULL, expires_at TIMESTAMP NULL,
// revoked_at TIMESTAMP NULL, created_at TIMESTAMP). Tables created before
// revoked_at existed need TokensRevokedAtMigration; until then Find
// refuses every token.
type Tokens struct {
	// Prefix starts every newly issued token ("vk_"), so secret scanners
	// (GitHub push protection, gitleaks) can match leaked tokens. It is up
	// to 32 letters, digits or underscores and ends with "_". Tokens issued
	// without it, or under an earlier prefix, stay valid.
	Prefix string
	// Now is the clock for issue, expiry and last-use times (default
	// time.Now). Set it to app.Now so a fake clock (vukem.WithClock,
	// vukemtest.Options.Clock) drives token expiry.
	Now func() time.Time
	// Log receives the one-time hint that access_tokens lacks revoked_at
	// (default slog.Default()); the token guard uses the request logger.
	Log *slog.Logger

	ex    database.Executor
	table string
	// outdatedLogged keeps the missing revoked_at hint to one log line.
	outdatedLogged atomic.Bool
	// revokedAtSeen skips the column probe once revoked_at was found.
	revokedAtSeen atomic.Bool
}

// errNoRevokedAt marks a lookup that failed because access_tokens predates
// the revoked_at column.
var errNoRevokedAt = errors.New("auth: access_tokens has no revoked_at column")

// outdatedHint is logged once per Tokens when access_tokens predates
// revoked_at.
const outdatedHint = "auth: access_tokens has no revoked_at column, so every API token is refused and " +
	"revoking deletes the row; add auth.TokensRevokedAtMigration to the app's migrations and run vukem migrate"

// sqlStater is implemented by driver errors carrying an SQLSTATE code
// (*pgconn.PgError).
type sqlStater interface {
	error
	SQLState() string
}

// missingRevokedAt reports whether err is the database saying the
// revoked_at column does not exist: SQLSTATE 42703 (PostgreSQL), "no such
// column" (SQLite) or error 1054 (MySQL/MariaDB), naming that column.
func missingRevokedAt(err error) bool {
	msg := err.Error()
	if !strings.Contains(msg, "revoked_at") {
		return false
	}

	if se, ok := errors.AsType[sqlStater](err); ok {
		return se.SQLState() == "42703"
	}

	return strings.Contains(msg, "no such column: revoked_at") || strings.Contains(msg, "Error 1054")
}

// logOutdated logs outdatedHint the first time it is called on t.
func (t *Tokens) logOutdated(ctx context.Context, log *slog.Logger, attrs ...any) {
	if !t.outdatedLogged.CompareAndSwap(false, true) {
		return
	}

	if log == nil {
		log = t.Log
	}

	if log == nil {
		log = slog.Default()
	}

	log.ErrorContext(ctx, outdatedHint, attrs...)
}

// NewTokens uses the access_tokens table.
func NewTokens(ex database.Executor) *Tokens {
	return &Tokens{ex: ex, table: "access_tokens", Now: time.Now}
}

func (t *Tokens) now() time.Time {
	if t.Now == nil {
		return time.Now()
	}

	return t.Now()
}

func (t *Tokens) q(sqlText string) string {
	return (&RememberStore{ex: t.ex, table: t.table}).q(sqlText)
}

// Create issues a token for userID and returns the plain value, shown to
// the client once. ttl 0 means the token never expires and stays valid
// until revoked; pass a ttl unless that is intended (the install:api
// scaffold issues 30-day tokens).
func (t *Tokens) Create(
	ctx context.Context,
	userID, name string,
	abilities []string,
	ttl time.Duration,
) (string, Token, error) {
	if err := validPrefix(t.Prefix); err != nil {
		return "", Token{}, err
	}

	now := t.now().UTC()
	tok := Token{ID: rand.Text(), UserID: userID, Name: name, Abilities: abilities}

	if ttl > 0 {
		exp := now.Add(ttl)
		tok.ExpiresAt = &exp
	}

	if tok.Abilities == nil {
		tok.Abilities = []string{}
	}

	abil, err := json.Marshal(tok.Abilities)
	if err != nil {
		return "", Token{}, err
	}

	secret := rand.Text()

	_, err = t.ex.ExecContext(
		ctx,
		t.q("INSERT INTO {table} (id, user_id, name, hash, abilities, expires_at, created_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?)"),
		tok.ID,
		userID,
		name,
		digest(secret),
		string(abil),
		tok.ExpiresAt,
		now,
	)
	if err != nil {
		return "", Token{}, err
	}

	return t.Prefix + tok.ID + "." + secret, tok, nil
}

// maxPrefix bounds Tokens.Prefix.
const maxPrefix = 32

func validPrefix(p string) error {
	if p == "" {
		return nil
	}

	ok := len(p) <= maxPrefix && strings.HasSuffix(p, "_") && !strings.ContainsFunc(p, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_'
	})
	if !ok {
		return fmt.Errorf(
			"auth: token prefix %q: want up to %d letters, digits or underscores ending in _",
			p,
			maxPrefix,
		)
	}

	return nil
}

// Find validates a plain token and returns it, or an error for an unknown,
// wrong, expired or revoked token (or a failed lookup). Every token pays
// for the same digest comparison before its state is checked. It updates
// last_used_at at most once a minute.
func (t *Tokens) Find(ctx context.Context, plain string) (Token, error) {
	id, secret, ok := strings.Cut(plain, ".")
	// Ids come from rand.Text (base32, no "_"), so whatever precedes the
	// last "_" is a prefix: the current one, an earlier one, or none.
	if i := strings.LastIndexByte(id, '_'); i >= 0 {
		id = id[i+1:]
	}

	if !ok || id == "" || secret == "" {
		return Token{}, errBadToken
	}

	var (
		tok    = Token{ID: id}
		stored string
		abil   string
	)

	err := t.ex.QueryRowContext(ctx, t.q("SELECT user_id, name, hash, abilities, last_used_at, expires_at, revoked_at "+
		"FROM {table} WHERE id = ?"), id).
		Scan(&tok.UserID, &tok.Name, &stored, &abil, &tok.LastUsedAt, &tok.ExpiresAt, &tok.RevokedAt)
	found := err == nil

	if !found {
		// An unknown id compares against a digest-sized placeholder, so it
		// costs what a known one does.
		stored = strings.Repeat("0", len(digest(secret)))
	}

	now := t.now().UTC()
	match := subtle.ConstantTimeCompare([]byte(stored), []byte(digest(secret))) == 1
	live := tok.RevokedAt == nil && (tok.ExpiresAt == nil || tok.ExpiresAt.After(now))

	if !found || !match || !live {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			if missingRevokedAt(err) {
				return Token{}, fmt.Errorf("auth: find token: %w: %w", errNoRevokedAt, err)
			}

			return Token{}, fmt.Errorf("auth: find token: %w", err)
		}

		return Token{}, errBadToken
	}

	if err := json.Unmarshal([]byte(abil), &tok.Abilities); err != nil {
		return Token{}, errBadToken
	}

	if tok.LastUsedAt == nil || now.Sub(*tok.LastUsedAt) > time.Minute {
		if _, err := t.ex.ExecContext(
			ctx,
			t.q("UPDATE {table} SET last_used_at = ? WHERE id = ?"),
			now,
			id,
		); err == nil {
			tok.LastUsedAt = &now
		}
	}

	return tok, nil
}

// Revoke marks a token revoked by id without checking who owns it; the row
// stays (List shows it) until Prune deletes it, and a second revoke keeps
// the first time. Use RevokeFor when the id comes from user input (a
// "revoke" button in a dashboard).
func (t *Tokens) Revoke(ctx context.Context, id string) error {
	_, err := t.revoke(ctx, "id = ?", id)
	return err
}

// RevokeFor revokes token id only if it belongs to userID and reports
// whether a live token was revoked (false for another user's token, an
// unknown id or one already revoked), so a caller cannot revoke another
// user's token by guessing its id.
func (t *Tokens) RevokeFor(ctx context.Context, userID, id string) (bool, error) {
	n, err := t.revoke(ctx, "id = ? AND user_id = ?", id, userID)
	return n > 0, err
}

// revoke sets revoked_at on the live tokens matching where. On a table
// without the column it deletes them instead, the safe side of a revoke;
// the column is probed first because a failed UPDATE would abort an
// enclosing PostgreSQL transaction (the install:auth password reset).
func (t *Tokens) revoke(ctx context.Context, where string, args ...any) (int64, error) {
	if !t.revokedAtSeen.Load() {
		has, err := schema.New(ctx, t.ex).HasColumn(t.table, "revoked_at")
		if err != nil {
			return 0, fmt.Errorf("auth: revoke token: %w", err)
		}

		if !has {
			t.logOutdated(ctx, nil)

			res, err := t.ex.ExecContext(ctx, t.q("DELETE FROM {table} WHERE "+where), args...)
			if err != nil {
				return 0, fmt.Errorf("auth: revoke token: %w", err)
			}

			return res.RowsAffected()
		}

		t.revokedAtSeen.Store(true)
	}

	res, err := t.ex.ExecContext(
		ctx,
		t.q("UPDATE {table} SET revoked_at = ? WHERE "+where+" AND revoked_at IS NULL"),
		append([]any{t.now().UTC()}, args...)...,
	)
	if err != nil {
		return 0, fmt.Errorf("auth: revoke token: %w", err)
	}

	return res.RowsAffected()
}

// List returns userID's tokens, newest first, revoked ones included with
// RevokedAt set (never the secrets). Every Token carries the UserID stored on
// its row, like Find, so the owner does not depend on what the caller asked
// for.
func (t *Tokens) List(ctx context.Context, userID string) ([]Token, error) {
	rows, err := t.ex.QueryContext(
		ctx,
		t.q("SELECT id, user_id, name, abilities, last_used_at, expires_at, revoked_at FROM {table} "+
			"WHERE user_id = ? ORDER BY created_at DESC, id"),
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Token

	for rows.Next() {
		var (
			tok  Token
			abil string
		)

		if err := rows.Scan(&tok.ID, &tok.UserID, &tok.Name, &abil,
			&tok.LastUsedAt, &tok.ExpiresAt, &tok.RevokedAt); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(abil), &tok.Abilities); err != nil {
			return nil, fmt.Errorf("auth: token %s abilities: %w", tok.ID, err)
		}

		out = append(out, tok)
	}

	return out, rows.Err()
}

// RevokeAll revokes every live token of userID.
func (t *Tokens) RevokeAll(ctx context.Context, userID string) error {
	_, err := t.revoke(ctx, "user_id = ?", userID)
	return err
}

// Prune deletes the tokens revoked or expired before cutoff and returns how
// many it deleted. Schedule it to bound the table while keeping recent
// revocations visible in List. Expiry is compared with the earlier of
// cutoff and now, so a cutoff in the future never deletes a live token.
func (t *Tokens) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	cutoff = cutoff.UTC()
	expired := cutoff
	if now := t.now().UTC(); now.Before(expired) {
		expired = now
	}

	res, err := t.ex.ExecContext(
		ctx,
		t.q("DELETE FROM {table} WHERE (revoked_at IS NOT NULL AND revoked_at < ?) "+
			"OR (expires_at IS NOT NULL AND expires_at < ?)"),
		cutoff,
		expired,
	)
	if err != nil {
		return 0, fmt.Errorf("auth: prune tokens: %w", err)
	}

	return res.RowsAffected()
}

// TokensRevokedAtMigration returns migration id adding the revoked_at column
// to an access_tokens table created before v0.26.0. It skips a table that
// already has it (the current install:auth scaffold), and its Down is a
// no-op, since the column may predate it.
func TokensRevokedAtMigration(id string) schema.Migration {
	return schema.Migration{
		ID: id,
		Up: func(s *schema.Schema) error {
			has, err := s.HasColumn("access_tokens", "revoked_at")
			if err != nil || has {
				return err
			}

			return s.Table("access_tokens", func(t *schema.Table) { t.Timestamp("revoked_at").Nullable() })
		},
		Down: func(*schema.Schema) error { return nil },
	}
}

// TokenGuard authenticates "Authorization: Bearer <token>" requests, plus
// any header WithTokenHeaders names.
type TokenGuard[U Authenticatable] struct {
	name     string
	provider UserProvider[U]
	tokens   *Tokens
	// headers is nil until WithTokenHeaders names a header, so Authorization
	// is the only credential source unless the app opts in. It is held
	// behind a pointer to keep TokenGuard comparable, which a slice field
	// would end (docs/stability.md, the apidiff gate).
	headers *tokenHeaders
}

// tokenHeaders lists the extra headers a guard reads a bare token from, as
// the app spelled them and in the order it named them.
type tokenHeaders struct{ names []string }

// NewTokenGuard returns the "api" style guard. It reads the token from
// "Authorization: Bearer <token>" only; WithTokenHeaders adds more headers.
func NewTokenGuard[U Authenticatable](name string, p UserProvider[U], tokens *Tokens) *TokenGuard[U] {
	return &TokenGuard[U]{name: name, provider: p, tokens: tokens}
}

// WithTokenHeaders also accepts the token from each named header, such as the
// "X-API-Key" convention, and returns g so it can be chained onto
// NewTokenGuard. Such a header carries the token alone, without a scheme.
//
// Precedence: a bearer token in "Authorization" always wins, so opting in
// never changes what a request that already worked resolves to; the named
// headers are then tried in the order given, and one sent on more than one
// line is skipped rather than guessed at. Every named header is added to the
// response's Vary on every request the guard sees, carrying it or not, so a
// shared cache cannot serve one key's response to a request holding another.
// "Authorization" needs no Vary entry: RFC 9111 §3.5 already keeps shared
// caches from storing a response to a request that carries it.
//
// No extra header is accepted without this call. It panics on a name that is
// not an HTTP field name, on "Authorization" (always read, and only as
// "Bearer <token>") and on "Cookie", since a token the browser attaches by
// itself would authenticate every cross-site request.
func (g *TokenGuard[U]) WithTokenHeaders(names ...string) *TokenGuard[U] {
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if !validHeaderName(name) {
			panic(fmt.Sprintf("auth: token header %q is not a valid HTTP header name", raw))
		}

		switch http.CanonicalHeaderKey(name) {
		case "Authorization":
			panic(`auth: token header "Authorization" is always read, as "Bearer <token>"`)
		case "Cookie":
			panic(`auth: token header "Cookie" would authenticate every cross-site request; ` +
				`use a session guard for cookie authentication`)
		}

		if g.headers == nil {
			g.headers = &tokenHeaders{}
		}

		// The name is kept as the app spelled it, since that is what Vary
		// shows; header lookups canonicalize it themselves.
		if !slices.ContainsFunc(g.headers.names, func(h string) bool { return strings.EqualFold(h, name) }) {
			g.headers.names = append(g.headers.names, name)
		}
	}

	return g
}

// tokenNames returns the extra header names, nil when the app named none.
func (g *TokenGuard[U]) tokenNames() []string {
	if g.headers == nil {
		return nil
	}

	return g.headers.names
}

// fieldNamePunct are the punctuation characters of an RFC 9110 token, the
// rest being letters and digits.
const fieldNamePunct = "!#$%&'*+-.^_`|~"

// validHeaderName reports whether name is a non-empty RFC 9110 token.
func validHeaderName(name string) bool {
	return name != "" && !strings.ContainsFunc(name, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') &&
			!strings.ContainsRune(fieldNamePunct, r)
	})
}

// token returns the credential of r: a bearer token in Authorization first,
// then each header of WithTokenHeaders in order. An extra header holds the
// bare token, trimmed; it is skipped when blank or sent on several lines,
// where picking one of two keys would be a guess.
func (g *TokenGuard[U]) token(r *http.Request) (string, bool) {
	if plain, ok := BearerToken(r); ok {
		return plain, true
	}

	for _, name := range g.tokenNames() {
		if vals := r.Header.Values(name); len(vals) == 1 {
			if plain := strings.TrimSpace(vals[0]); plain != "" {
				return plain, true
			}
		}
	}

	return "", false
}

// vary marks the response as depending on every extra token header, whether
// this request carried it or not: a cache that ignores it could serve one
// key's response to a request holding another, or none.
func (g *TokenGuard[U]) vary(c *web.Ctx) {
	names := g.tokenNames()
	if len(names) == 0 {
		return
	}

	h := c.Writer().Header()

	for _, name := range names {
		if !varies(h, name) {
			h.Add("Vary", name)
		}
	}
}

// varies reports whether h already lists name in Vary, over every header line
// and every comma-separated item of one.
func varies(h http.Header, name string) bool {
	for _, line := range h.Values("Vary") {
		for item := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.TrimSpace(item), name) {
				return true
			}
		}
	}

	return false
}

// Name implements Checker.
func (g *TokenGuard[U]) Name() string { return g.name }

// APIOnly implements APIGuard.
func (*TokenGuard[U]) APIOnly() {}

type tokenKey struct{ guard string }

// Check implements Checker.
func (g *TokenGuard[U]) Check(c *web.Ctx) bool {
	_, err := g.User(c)
	return err == nil
}

// authenticate tells a missing, wrong, expired or revoked token
// (ErrUnauthenticated) apart from a failing store, which Required answers
// with 503.
func (g *TokenGuard[U]) authenticate(c *web.Ctx) error {
	_, err := g.User(c)
	return err
}

// User returns the token's user (cached for the request), ErrUnauthenticated
// for a missing, unknown, wrong, expired or revoked token, or a 503 error
// when the token or user lookup fails (database down, access_tokens
// without revoked_at), so an outage is not reported as bad credentials. The
// token comes from "Authorization: Bearer <token>" and from the headers of
// WithTokenHeaders, which the response's Vary then names.
func (g *TokenGuard[U]) User(c *web.Ctx) (U, error) {
	g.vary(c)

	if u, ok := cached[U](c, g.name); ok {
		return u, nil
	}

	var zero U

	plain, ok := g.token(c.Request())
	if !ok {
		return zero, ErrUnauthenticated
	}

	tok, err := g.tokens.Find(c.Context(), plain)

	switch {
	case err == nil:
	case errors.Is(err, errBadToken):
		web.SecurityEvent(c.Request(), "token.rejected", "guard", g.name)
		return zero, ErrUnauthenticated
	default:
		if errors.Is(err, errNoRevokedAt) {
			g.tokens.logOutdated(c.Context(), c.Log(), "guard", g.name)
		}

		return zero, errs.Wrap(http.StatusServiceUnavailable, err)
	}

	u, err := g.provider.ByID(c.Context(), tok.UserID)
	if err != nil {
		if isNotFound(err) {
			return zero, ErrUnauthenticated
		}

		return zero, errs.Wrap(http.StatusServiceUnavailable, fmt.Errorf("auth: load user: %w", err))
	}

	cache(c, g.name, u)
	c.SetRequest(c.Request().WithContext(context.WithValue(c.Context(), tokenKey{g.name}, tok)))

	return u, nil
}

// BearerToken returns the credential of an "Authorization: Bearer <token>"
// header, trimmed. The scheme is matched case-insensitively (RFC 7235
// §2.1), so "bearer" and "BEARER" work too; ok is false without a bearer
// header or with an empty token.
func BearerToken(r *http.Request) (token string, ok bool) {
	scheme, rest, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}

	token = strings.TrimSpace(rest)

	return token, token != ""
}

// CurrentToken returns the token that authenticated c with guard.
func CurrentToken(c *web.Ctx, guard string) (Token, bool) {
	tok, ok := c.Context().Value(tokenKey{guard}).(Token)
	return tok, ok
}

// TokenCan reports whether the request's token (any token guard) grants ability.
func TokenCan(c *web.Ctx, guard, ability string) bool {
	tok, ok := CurrentToken(c, guard)
	return ok && tok.Can(ability)
}
