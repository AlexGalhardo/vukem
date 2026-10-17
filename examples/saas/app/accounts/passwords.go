package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/examples/saas/app/mail"
	"github.com/AlexGalhardo/vukem/examples/saas/app/models"
)

// SendResetLink emails a single-use password reset link. Unknown emails
// are silently ignored so the form never reveals who has an account.
func (a *Accounts) SendResetLink(ctx context.Context, email string) error {
	email = strings.ToLower(email)

	u, err := a.Users.ByCredentials(ctx, map[string]string{"email": email})
	if errors.Is(err, auth.ErrUserNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	token := rand.Text()
	d := a.DB.Dialect()

	if _, err := a.DB.ExecContext(
		ctx,
		"DELETE FROM password_resets WHERE email = "+d.Placeholder(1),
		email,
	); err != nil {
		return err
	}

	if _, err := a.DB.ExecContext(ctx, "INSERT INTO password_resets (email, hash, created_at) VALUES ("+
		d.Placeholder(
			1,
		)+", "+d.Placeholder(2)+", "+d.Placeholder(3)+")", email, digest(token), a.now().UTC()); err != nil {
		return err
	}

	link := a.URL + "/reset-password/" + token + "?email=" + url.QueryEscape(email)

	return a.Mailer.Send(ctx, mail.ResetPassword{User: u, Link: link})
}

var errInvalidReset = errs.NewValidation().Add("email", "This password reset link is invalid or has expired.")

// ResetPassword checks the token (constant time, single use, ResetTTL) and
// sets the new password, logging out every remembered session.
func (a *Accounts) ResetPassword(ctx context.Context, email, token, password string) (models.User, error) {
	email = strings.ToLower(email)
	d := a.DB.Dialect()

	var (
		stored  string
		created time.Time
	)

	err := a.DB.QueryRowContext(ctx, "SELECT hash, created_at FROM password_resets WHERE email = "+d.Placeholder(1), email).
		Scan(&stored, &created)
	if err != nil || subtle.ConstantTimeCompare([]byte(stored), []byte(digest(token))) != 1 ||
		a.expired(created, a.ResetTTL) {
		return models.User{}, errInvalidReset
	}

	u, err := a.Users.ByCredentials(ctx, map[string]string{"email": email})
	if err != nil {
		return models.User{}, errInvalidReset
	}

	err = a.DB.Tx(ctx, func(tx *database.Tx) error {
		if _, err := tx.ExecContext(
			ctx,
			"DELETE FROM password_resets WHERE email = "+d.Placeholder(1),
			email,
		); err != nil {
			return err
		}

		if _, err := tx.ExecContext(
			ctx,
			"DELETE FROM remember_tokens WHERE user_id = "+d.Placeholder(1),
			u.AuthID(),
		); err != nil {
			return err
		}

		return a.updatePassword(ctx, tx, u.ID, password)
	})

	return u, err
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
