package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
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
}

// Can reports whether the token grants ability ("*" grants everything).
func (t Token) Can(ability string) bool {
	return slices.Contains(t.Abilities, "*") || slices.Contains(t.Abilities, ability)
}

// Tokens issues and checks opaque access tokens "<prefix><id>.<secret>";
// only SHA-256(secret) is stored. Table (created by install:auth / install:api):
// access_tokens(id TEXT PRIMARY KEY, user_id TEXT, name TEXT, hash TEXT,
// abilities TEXT, last_used_at TIMESTAMP NULL, expires_at TIMESTAMP NULL,
// created_at TIMESTAMP).
type Tokens struct {
	// Prefix starts every newly issued token ("vk_"), so secret scanners
	// (GitHub push protection, gitleaks) can match leaked tokens. It is up
	// to 32 letters, digits or underscores and ends with "_". Tokens issued
	// without it, or under an earlier prefix, stay valid.
	Prefix string

	ex    database.Executor
	table string
	now   func() time.Time
}

// NewTokens uses the access_tokens table.
func NewTokens(ex database.Executor) *Tokens {
	return &Tokens{ex: ex, table: "access_tokens", now: time.Now}
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

// Find validates a plain token and returns it, or errBadToken. It updates
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

	err := t.ex.QueryRowContext(ctx, t.q("SELECT user_id, name, hash, abilities, last_used_at, expires_at FROM {table} WHERE id = ?"),
		id).
		Scan(&tok.UserID, &tok.Name, &stored, &abil, &tok.LastUsedAt, &tok.ExpiresAt)
	if err != nil {
		return Token{}, errBadToken
	}

	now := t.now().UTC()

	if subtle.ConstantTimeCompare([]byte(stored), []byte(digest(secret))) != 1 ||
		tok.ExpiresAt != nil && !tok.ExpiresAt.After(now) {
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

// Revoke deletes a token by id without checking who owns it. Use RevokeFor
// when the id comes from user input (a "revoke" button in a dashboard).
func (t *Tokens) Revoke(ctx context.Context, id string) error {
	_, err := t.ex.ExecContext(ctx, t.q("DELETE FROM {table} WHERE id = ?"), id)
	return err
}

// RevokeFor deletes token id only if it belongs to userID and reports
// whether a token was deleted, so a caller cannot revoke another user's
// token by guessing its id.
func (t *Tokens) RevokeFor(ctx context.Context, userID, id string) (bool, error) {
	res, err := t.ex.ExecContext(ctx, t.q("DELETE FROM {table} WHERE id = ? AND user_id = ?"), id, userID)
	if err != nil {
		return false, err
	}

	n, err := res.RowsAffected()

	return n > 0, err
}

// List returns userID's tokens, newest first (never the secrets).
func (t *Tokens) List(ctx context.Context, userID string) ([]Token, error) {
	rows, err := t.ex.QueryContext(
		ctx,
		t.q("SELECT id, name, abilities, last_used_at, expires_at FROM {table} WHERE user_id = ? "+
			"ORDER BY created_at DESC, id"),
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Token

	for rows.Next() {
		tok := Token{UserID: userID}

		var abil string
		if err := rows.Scan(&tok.ID, &tok.Name, &abil, &tok.LastUsedAt, &tok.ExpiresAt); err != nil {
			return nil, err
		}

		if err := json.Unmarshal([]byte(abil), &tok.Abilities); err != nil {
			return nil, fmt.Errorf("auth: token %s abilities: %w", tok.ID, err)
		}

		out = append(out, tok)
	}

	return out, rows.Err()
}

// RevokeAll deletes every token of userID.
func (t *Tokens) RevokeAll(ctx context.Context, userID string) error {
	_, err := t.ex.ExecContext(ctx, t.q("DELETE FROM {table} WHERE user_id = ?"), userID)
	return err
}

// TokenGuard authenticates "Authorization: Bearer <token>" requests.
type TokenGuard[U Authenticatable] struct {
	name     string
	provider UserProvider[U]
	tokens   *Tokens
}

// NewTokenGuard returns the "api" style guard.
func NewTokenGuard[U Authenticatable](name string, p UserProvider[U], tokens *Tokens) *TokenGuard[U] {
	return &TokenGuard[U]{name: name, provider: p, tokens: tokens}
}

// Name implements Checker.
func (g *TokenGuard[U]) Name() string { return g.name }

type tokenKey struct{ guard string }

// Check implements Checker.
func (g *TokenGuard[U]) Check(c *web.Ctx) bool {
	_, err := g.User(c)
	return err == nil
}

// User returns the token's user (cached for the request) or
// ErrUnauthenticated.
func (g *TokenGuard[U]) User(c *web.Ctx) (U, error) {
	if u, ok := cached[U](c, g.name); ok {
		return u, nil
	}

	var zero U

	plain, ok := strings.CutPrefix(c.Header("Authorization"), "Bearer ")
	if !ok || plain == "" {
		return zero, ErrUnauthenticated
	}

	tok, err := g.tokens.Find(c.Context(), strings.TrimSpace(plain))
	if err != nil {
		web.SecurityEvent(c.Request(), "token.rejected", "guard", g.name)
		return zero, ErrUnauthenticated
	}

	u, err := g.provider.ByID(c.Context(), tok.UserID)
	if err != nil {
		return zero, ErrUnauthenticated
	}

	cache(c, g.name, u)
	c.SetRequest(c.Request().WithContext(context.WithValue(c.Context(), tokenKey{g.name}, tok)))

	return u, nil
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
