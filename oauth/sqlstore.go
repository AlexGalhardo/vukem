package oauth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/schema"
)

// SQLStore is a Store on the app database; it uses only portable SQL and the
// dialect placeholders, so it runs on SQLite, PostgreSQL and MySQL. Create
// the tables with Migration.
type SQLStore struct {
	ex     database.Executor
	tenant bool
}

// NewSQLStore returns a Store over ex.
func NewSQLStore(ex database.Executor) *SQLStore { return &SQLStore{ex: ex} }

// WithTenant returns a SQLStore that also stores Grant.Tenant, in the column
// TenantMigration adds. Without it a Grant with a Tenant is refused rather
// than saved without it, and Grant.Tenant is always empty.
func (s *SQLStore) WithTenant() *SQLStore { return &SQLStore{ex: s.ex, tenant: true} }

// TenantMigration adds the nullable tenant column to oauth_grants, for apps
// that use ConsentDecider with Decision.Tenant. Run it after Migration, which
// stays as it was so its checksum does not change.
func TenantMigration(id string) schema.Migration {
	return schema.Migration{
		ID: id,
		Up: func(s *schema.Schema) error {
			return s.Table("oauth_grants", func(t *schema.Table) { t.String("tenant", 255).Nullable() })
		},
		Down: func(s *schema.Schema) error {
			return s.Table("oauth_grants", func(t *schema.Table) { t.DropColumn("tenant") })
		},
	}
}

// Migration creates oauth_grants, oauth_codes and oauth_refresh_tokens.
func Migration(id string) schema.Migration {
	return schema.Migration{
		ID: id,
		Up: func(s *schema.Schema) error {
			if err := s.Create("oauth_grants", func(t *schema.Table) {
				t.String("id", 64).Unique()
				t.String("client_id", 512)
				t.String("subject", 255)
				t.String("scopes", 1024)
				t.String("resource", 512)
				t.Timestamp("created_at")
				t.Timestamp("revoked_at").Nullable()
				t.Index("subject")
			}); err != nil {
				return err
			}

			if err := s.Create("oauth_codes", func(t *schema.Table) {
				t.String("hash", 64).Unique()
				t.String("grant_id", 64)
				t.String("client_id", 512)
				t.String("redirect_uri", 2048)
				t.String("challenge", 128)
				t.Timestamp("expires_at")
				t.Timestamp("used_at").Nullable()
			}); err != nil {
				return err
			}

			return s.Create("oauth_refresh_tokens", func(t *schema.Table) {
				t.String("hash", 64).Unique()
				t.String("grant_id", 64)
				t.Timestamp("expires_at")
				t.Timestamp("used_at").Nullable()
				t.Index("grant_id")
			})
		},
		Down: func(s *schema.Schema) error {
			for _, t := range []string{"oauth_refresh_tokens", "oauth_codes", "oauth_grants"} {
				if err := s.Drop(t); err != nil {
					return err
				}
			}

			return nil
		},
	}
}

func (s *SQLStore) q(text string) string {
	d := s.ex.Dialect()
	n := 0

	var b strings.Builder

	for _, ch := range text {
		if ch == '?' {
			n++
			b.WriteString(d.Placeholder(n))

			continue
		}

		b.WriteRune(ch)
	}

	return b.String()
}

// CreateGrant implements Store.
func (s *SQLStore) CreateGrant(ctx context.Context, g Grant) error {
	var err error

	switch {
	case s.tenant:
		_, err = s.ex.ExecContext(ctx, s.q(
			"INSERT INTO oauth_grants (id, client_id, subject, scopes, resource, created_at, tenant) VALUES (?, ?, ?, ?, ?, ?, ?)",
		),
			g.ID, g.ClientID, g.Subject, strings.Join(g.Scopes, " "), g.Resource, g.CreatedAt.UTC(), g.Tenant)
	case g.Tenant != "":
		return errors.New("oauth: grant has a tenant; use SQLStore.WithTenant after TenantMigration")
	default:
		_, err = s.ex.ExecContext(ctx, s.q(
			"INSERT INTO oauth_grants (id, client_id, subject, scopes, resource, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		),
			g.ID, g.ClientID, g.Subject, strings.Join(g.Scopes, " "), g.Resource, g.CreatedAt.UTC())
	}

	if err != nil {
		return fmt.Errorf("oauth: create grant: %w", err)
	}

	return nil
}

// Grant implements Store.
func (s *SQLStore) Grant(ctx context.Context, id string) (Grant, error) {
	var (
		g       Grant
		scopes  string
		revoked sql.NullTime
		tenant  sql.NullString
	)

	cols, dest := "", []any{&g.ID, &g.ClientID, &g.Subject, &scopes, &g.Resource, &g.CreatedAt, &revoked}
	if s.tenant {
		cols, dest = ", tenant", append(dest, &tenant)
	}

	err := s.ex.QueryRowContext(ctx, s.q(
		"SELECT id, client_id, subject, scopes, resource, created_at, revoked_at"+cols+" FROM oauth_grants WHERE id = ?",
	),
		id).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, ErrNotFound
	}

	if err != nil {
		return Grant{}, fmt.Errorf("oauth: load grant: %w", err)
	}

	g.Scopes, g.Revoked, g.Tenant = strings.Fields(scopes), revoked.Valid, tenant.String

	return g, nil
}

// SaveCode implements Store.
func (s *SQLStore) SaveCode(ctx context.Context, c Code) error {
	_, err := s.ex.ExecContext(ctx, s.q(
		"INSERT INTO oauth_codes (hash, grant_id, client_id, redirect_uri, challenge, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
	),
		c.Hash, c.GrantID, c.ClientID, c.RedirectURI, c.Challenge, c.ExpiresAt.UTC())
	if err != nil {
		return fmt.Errorf("oauth: save code: %w", err)
	}

	return nil
}

// RevokeGrant implements Store.
func (s *SQLStore) RevokeGrant(ctx context.Context, id string) error {
	_, err := s.ex.ExecContext(ctx, s.q(
		"UPDATE oauth_grants SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL"), time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("oauth: revoke grant: %w", err)
	}

	return nil
}

// RedeemCode implements Store. The UPDATE ... WHERE used_at IS NULL is the
// atomic step: of two concurrent redemptions one updates the row.
func (s *SQLStore) RedeemCode(ctx context.Context, hash string, now time.Time) (Code, error) {
	c := Code{Hash: hash}

	var used sql.NullTime

	err := s.ex.QueryRowContext(ctx, s.q(
		"SELECT grant_id, client_id, redirect_uri, challenge, expires_at, used_at FROM oauth_codes WHERE hash = ?",
	), hash).
		Scan(&c.GrantID, &c.ClientID, &c.RedirectURI, &c.Challenge, &c.ExpiresAt, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return Code{}, ErrNotFound
	}

	if err != nil {
		return Code{}, fmt.Errorf("oauth: load code: %w", err)
	}

	if used.Valid {
		return c, s.reused(ctx, c.GrantID)
	}

	res, err := s.ex.ExecContext(
		ctx,
		s.q("UPDATE oauth_codes SET used_at = ? WHERE hash = ? AND used_at IS NULL"),
		now.UTC(),
		hash,
	)
	if err != nil {
		return Code{}, fmt.Errorf("oauth: consume code: %w", err)
	}

	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return c, s.reused(ctx, c.GrantID)
	}

	if !now.Before(c.ExpiresAt) {
		return Code{}, ErrExpired
	}

	return c, nil
}

func (s *SQLStore) reused(ctx context.Context, grantID string) error {
	if err := s.RevokeGrant(ctx, grantID); err != nil {
		return err
	}

	return ErrReused
}

// SaveRefresh implements Store.
func (s *SQLStore) SaveRefresh(ctx context.Context, r Refresh) error {
	_, err := s.ex.ExecContext(ctx, s.q(
		"INSERT INTO oauth_refresh_tokens (hash, grant_id, expires_at) VALUES (?, ?, ?)"),
		r.Hash, r.GrantID, r.ExpiresAt.UTC())
	if err != nil {
		return fmt.Errorf("oauth: save refresh token: %w", err)
	}

	return nil
}

// GrantOfRefresh implements Store.
func (s *SQLStore) GrantOfRefresh(ctx context.Context, hash string) (string, error) {
	var id string

	err := s.ex.QueryRowContext(ctx, s.q("SELECT grant_id FROM oauth_refresh_tokens WHERE hash = ?"), hash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("oauth: load refresh token: %w", err)
	}

	return id, nil
}

// RotateRefresh implements Store.
func (s *SQLStore) RotateRefresh(ctx context.Context, oldHash string, next Refresh, now time.Time) (string, error) {
	var (
		grantID string
		expires time.Time
		used    sql.NullTime
	)

	err := s.ex.QueryRowContext(ctx, s.q(
		"SELECT grant_id, expires_at, used_at FROM oauth_refresh_tokens WHERE hash = ?"), oldHash).
		Scan(&grantID, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("oauth: load refresh token: %w", err)
	}

	if g, err := s.Grant(ctx, grantID); err != nil || g.Revoked {
		if err != nil {
			return "", err
		}

		return "", ErrRevoked
	}

	switch {
	case used.Valid:
		return "", s.reused(ctx, grantID)
	case !now.Before(expires):
		return "", ErrExpired
	}

	res, err := s.ex.ExecContext(ctx, s.q(
		"UPDATE oauth_refresh_tokens SET used_at = ? WHERE hash = ? AND used_at IS NULL"), now.UTC(), oldHash)
	if err != nil {
		return "", fmt.Errorf("oauth: consume refresh token: %w", err)
	}

	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return "", s.reused(ctx, grantID)
	}

	next.GrantID = grantID

	return grantID, s.SaveRefresh(ctx, next)
}
