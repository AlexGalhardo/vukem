package jwt

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

// ErrRefreshReused is returned when a refresh token is presented again
// after it was rotated: the whole token family is revoked, since one of
// its copies was stolen.
var ErrRefreshReused = errors.New("jwt: refresh token reused; family revoked")

// RefreshTokens stores opaque, rotating refresh tokens (only their SHA-256
// is kept). Each Rotate consumes the presented token and returns a new one
// of the same family; presenting a consumed token revokes the family.
type RefreshTokens struct {
	ex    database.Executor
	table string
	// TTL is the lifetime of each token (default 30 days).
	TTL time.Duration
	// Now is the clock (default time.Now).
	Now func() time.Time
}

// NewRefreshTokens uses the refresh_tokens table (RefreshTokensMigration).
func NewRefreshTokens(ex database.Executor) *RefreshTokens {
	return &RefreshTokens{ex: ex, table: "refresh_tokens", TTL: 30 * 24 * time.Hour, Now: time.Now}
}

// RefreshTokensMigration creates the refresh_tokens table.
func RefreshTokensMigration(id string) schema.Migration {
	return schema.Migration{
		ID: id,
		Up: func(s *schema.Schema) error {
			return s.Create("refresh_tokens", func(t *schema.Table) {
				t.String("id", 64).Unique()
				t.String("user_id", 64)
				t.String("family_id", 64)
				t.String("token_hash", 64).Unique()
				t.Timestamp("expires_at")
				t.Timestamp("used_at").Nullable()
				t.Timestamp("revoked_at").Nullable()
				t.Timestamp("created_at")
				t.Index("family_id")
				t.Index("user_id")
			})
		},
		Down: func(s *schema.Schema) error { return s.Drop("refresh_tokens") },
	}
}

func (r *RefreshTokens) q(sqlText string) string {
	d := r.ex.Dialect()
	n := 0

	var b strings.Builder

	for _, ch := range strings.ReplaceAll(sqlText, "{table}", r.table) {
		if ch == '?' {
			n++
			b.WriteString(d.Placeholder(n))

			continue
		}

		b.WriteRune(ch)
	}

	return b.String()
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// Issue starts a new token family for userID and returns the plain token.
func (r *RefreshTokens) Issue(ctx context.Context, userID string) (string, error) {
	return r.insert(ctx, userID, rand.Text())
}

func (r *RefreshTokens) insert(ctx context.Context, userID, family string) (string, error) {
	plain := rand.Text() + rand.Text()
	now := r.Now().UTC()

	_, err := r.ex.ExecContext(
		ctx,
		r.q(
			"INSERT INTO {table} (id, user_id, family_id, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		),
		rand.Text(),
		userID,
		family,
		hashToken(plain),
		now.Add(r.TTL),
		now,
	)
	if err != nil {
		return "", fmt.Errorf("jwt: store refresh token: %w", err)
	}

	return plain, nil
}

// Rotate consumes plain and returns its user and a new token of the same
// family. Unknown, expired or revoked tokens give ErrInvalid; a token that
// was already consumed gives ErrRefreshReused and revokes its family.
func (r *RefreshTokens) Rotate(ctx context.Context, plain string) (userID, next string, err error) {
	var (
		family    string
		expiresAt time.Time
		usedAt    sql.NullTime
		revokedAt sql.NullTime
	)

	hash := hashToken(plain)

	err = r.ex.QueryRowContext(ctx,
		r.q("SELECT user_id, family_id, expires_at, used_at, revoked_at FROM {table} WHERE token_hash = ?"), hash).
		Scan(&userID, &family, &expiresAt, &usedAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", invalid("unknown refresh token")
	}

	if err != nil {
		return "", "", fmt.Errorf("jwt: load refresh token: %w", err)
	}

	now := r.Now().UTC()

	switch {
	case revokedAt.Valid:
		return "", "", invalid("refresh token revoked")
	case usedAt.Valid:
		return "", "", r.reused(ctx, family)
	case !now.Before(expiresAt):
		return "", "", invalid("refresh token expired")
	}

	// Consume atomically: of two concurrent rotations only one updates the
	// row; the other is treated as reuse.
	res, err := r.ex.ExecContext(
		ctx,
		r.q(
			"UPDATE {table} SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND revoked_at IS NULL",
		),
		now,
		hash,
	)
	if err != nil {
		return "", "", fmt.Errorf("jwt: consume refresh token: %w", err)
	}

	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return "", "", r.reused(ctx, family)
	}

	next, err = r.insert(ctx, userID, family)
	if err != nil {
		return "", "", err
	}

	return userID, next, nil
}

func (r *RefreshTokens) reused(ctx context.Context, family string) error {
	if err := r.revokeWhere(ctx, "family_id = ?", family); err != nil {
		return err
	}

	return fmt.Errorf("%w: %w", ErrInvalid, ErrRefreshReused)
}

// Revoke ends the family of plain (logout); unknown tokens are ignored.
func (r *RefreshTokens) Revoke(ctx context.Context, plain string) error {
	var family string

	err := r.ex.QueryRowContext(ctx, r.q("SELECT family_id FROM {table} WHERE token_hash = ?"), hashToken(plain)).
		Scan(&family)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}

	if err != nil {
		return err
	}

	return r.revokeWhere(ctx, "family_id = ?", family)
}

// RevokeUser ends every refresh token of userID (password change).
func (r *RefreshTokens) RevokeUser(ctx context.Context, userID string) error {
	return r.revokeWhere(ctx, "user_id = ?", userID)
}

func (r *RefreshTokens) revokeWhere(ctx context.Context, cond string, arg any) error {
	_, err := r.ex.ExecContext(ctx, r.q("UPDATE {table} SET revoked_at = ? WHERE revoked_at IS NULL AND "+cond),
		r.Now().UTC(), arg)
	if err != nil {
		return fmt.Errorf("jwt: revoke refresh tokens: %w", err)
	}

	return nil
}
