package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/examples/blog/app/mail"
	"github.com/AlexGalhardo/vukem/examples/blog/app/models"
	"github.com/AlexGalhardo/vukem/examples/blog/app/requests"
	"github.com/AlexGalhardo/vukem/orm"
)

// Register signs in.Email up without revealing whether it already has an
// account: the caller always answers "check your email" and never logs in.
// A new address gets the account and a verification mail; an existing one
// gets nothing and is left unchanged. Both paths hash the password first,
// so they take about the same time.
func (a *Accounts) Register(ctx context.Context, in requests.Register) error {
	email := strings.ToLower(strings.TrimSpace(in.Email))

	h, err := a.Hasher.Make(in.Password)
	if err != nil {
		return err
	}

	if _, err := orm.QueryOf[models.User](a.DB).Where("email", email).First(ctx); err == nil {
		return nil
	} else if !errors.Is(err, orm.ErrNotFound) {
		return err
	}

	u := models.User{Name: in.Name, Email: email, Password: h}
	if err := orm.Create(ctx, a.DB, &u); err != nil {
		// A concurrent sign-up with the same email won the unique index.
		if _, findErr := orm.QueryOf[models.User](a.DB).Where("email", email).First(ctx); findErr == nil {
			return nil
		}

		return err
	}

	return a.SendVerification(ctx, u)
}

// SendVerification emails a signed link that verifies u's address.
func (a *Accounts) SendVerification(ctx context.Context, u models.User) error {
	link, err := a.Signer.Sign(fmt.Sprintf("%s/email/verify/%d", a.URL, u.ID), a.VerifyTTL)
	if err != nil {
		return err
	}

	return a.Mailer.Send(ctx, mail.VerifyEmail{User: u, Link: link})
}

// MarkVerified sets email_verified_at using ex (a.DB, or the transaction
// in progress: SQLite has a single writer connection).
func (a *Accounts) MarkVerified(ctx context.Context, ex database.Executor, u *models.User) error {
	if u.EmailVerified() {
		return nil
	}

	u.EmailVerifiedAt = orm.NullTime{}
	u.EmailVerifiedAt.V, u.EmailVerifiedAt.Valid = a.now().UTC(), true

	_, err := orm.QueryOf[models.User](
		ex,
	).Where("id", u.ID).
		Update(ctx, orm.Set{"email_verified_at": u.EmailVerifiedAt.V})

	return err
}

// UpdatePassword stores a new hash for u inside tx.
func (a *Accounts) updatePassword(ctx context.Context, tx database.Executor, id int64, password string) error {
	h, err := a.Hasher.Make(password)
	if err != nil {
		return err
	}

	_, err = orm.QueryOf[models.User](tx).Where("id", id).Update(ctx, orm.Set{"password": h})

	return err
}

func (a *Accounts) expired(created time.Time, ttl time.Duration) bool {
	return a.now().After(created.Add(ttl))
}
