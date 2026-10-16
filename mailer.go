package vukem

import (
	"errors"
	"fmt"
	"sync"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/web"
)

// MailConfig is read from MAIL_* (and SMTP_* / RESEND_*) variables.
type MailConfig struct {
	// Mailer is "smtp", "resend", "log" or "array" (tests). Production
	// must choose explicitly; elsewhere it defaults to "log".
	Mailer string `env:"MAIL_MAILER"`
	From   string `env:"MAIL_FROM"   default:"Vukem <hello@example.com>"`

	SMTPHost     string        `env:"SMTP_HOST"     default:"localhost"`
	SMTPPort     int           `env:"SMTP_PORT"     default:"587"`
	SMTPUsername string        `env:"SMTP_USERNAME"`
	SMTPPassword config.Secret `env:"SMTP_PASSWORD"`
	// SMTPInsecure allows servers without STARTTLS (local catchers only).
	SMTPInsecure bool `env:"SMTP_INSECURE"`

	ResendAPIKey config.Secret `env:"RESEND_API_KEY"`
}

type mailState struct {
	once      sync.Once
	mailer    *mail.Mailer
	err       error
	transport mail.Transport
}

// WithMailTransport replaces the transport chosen by MAIL_MAILER.
func WithMailTransport(t mail.Transport) Option {
	return func(o *options) error { o.mailTransport = t; return nil }
}

// Mailer returns the app mailer, built from MAIL_* on first use. Views
// come from the app's view engine (WithViews).
func (a *App) Mailer() (*mail.Mailer, error) {
	a.mail.once.Do(func() {
		cfg, err := configLoad[MailConfig](a)
		if err != nil {
			a.mail.err = err
			return
		}

		t := a.mail.transport
		if t == nil {
			if t, err = a.mailTransport(cfg); err != nil {
				a.mail.err = err
				return
			}
		}

		var views web.Renderer
		if a.Views != nil {
			views = a.Views
		}

		a.mail.mailer = mail.New(t, cfg.From, views)
	})

	return a.mail.mailer, a.mail.err
}

func (a *App) mailTransport(cfg MailConfig) (mail.Transport, error) {
	switch cfg.Mailer {
	case "":
		if a.Config.IsProduction() {
			return nil, errors.New("vukem: set MAIL_MAILER in production (smtp, resend or log)")
		}

		return mail.Log{L: a.Log}, nil
	case "log":
		return mail.Log{L: a.Log}, nil
	case "array":
		return mail.NewArray(), nil
	case "smtp":
		return mail.NewSMTP(mail.SMTPConfig{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword,
			InsecureSkipTLS: cfg.SMTPInsecure,
		}), nil
	case "resend":
		if cfg.ResendAPIKey.IsZero() {
			return nil, errors.New("vukem: MAIL_MAILER=resend needs RESEND_API_KEY")
		}

		return mail.NewResend(mail.ResendConfig{APIKey: cfg.ResendAPIKey}), nil
	default:
		return nil, fmt.Errorf("vukem: unknown MAIL_MAILER %q", cfg.Mailer)
	}
}
