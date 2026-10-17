// Package accounts wires authentication: guards, tokens, password resets
// and email verification. It is your code (vukem install:auth): change the
// rules freely.
package accounts

import (
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/examples/saas/app/models"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/signed"
)

// Accounts holds the authentication services.
type Accounts struct {
	DB     *database.DB
	Mailer *mail.Mailer
	Users  *auth.ORMProvider[models.User]
	Web    *auth.SessionGuard[models.User]
	API    *auth.TokenGuard[models.User]
	Tokens *auth.Tokens
	Hasher hash.Hasher
	Signer *signed.Signer
	// URL is the public base URL used in emails (APP_URL).
	URL string
	// ResetTTL is how long password reset links work.
	ResetTTL time.Duration
	// VerifyTTL is how long email verification links work.
	VerifyTTL time.Duration
	now       func() time.Time
}

// New builds Accounts from the app.
func New(app *vukem.App) (*Accounts, error) {
	db, err := app.DB()
	if err != nil {
		return nil, err
	}

	mailer, err := app.Mailer()
	if err != nil {
		return nil, err
	}

	hasher := hash.Default()
	if app.Kind == vukem.KindTest {
		hasher = hash.Fake() // fast hashing keeps tests quick
	}

	users := auth.NewORMProvider[models.User](db)
	tokens := auth.NewTokens(db)

	web := auth.NewSessionGuard("web", users, hasher, auth.NewRememberStore(db))
	web.InsecureCookies = !app.Config.IsProduction() && !strings.HasPrefix(app.Config.URL, "https://")

	return &Accounts{
		DB: db, Mailer: mailer, Users: users, Web: web, Tokens: tokens, Hasher: hasher,
		API:       auth.NewTokenGuard("api", users, tokens),
		Signer:    signed.New(app.Key.Derive("signed.urls", 32)),
		URL:       strings.TrimSuffix(app.Config.URL, "/"),
		ResetTTL:  time.Hour,
		VerifyTTL: time.Hour,
		now:       time.Now,
	}, nil
}
