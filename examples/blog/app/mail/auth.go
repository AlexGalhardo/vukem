package mail

import (
	"github.com/AlexGalhardo/vukem/examples/blog/app/models"
	"github.com/AlexGalhardo/vukem/mail"
)

// VerifyEmail asks a new user to confirm their address.
type VerifyEmail struct {
	User models.User
	Link string
}

// Envelope implements mail.Mailable.
func (m VerifyEmail) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{m.User.Email}, Subject: "Verify your email address"}
}

// Content implements mail.Mailable.
func (m VerifyEmail) Content() mail.Content { return mail.View("mail/verify_email", m) }

// ResetPassword carries a password reset link.
type ResetPassword struct {
	User models.User
	Link string
}

// Envelope implements mail.Mailable.
func (m ResetPassword) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{m.User.Email}, Subject: "Reset your password"}
}

// Content implements mail.Mailable.
func (m ResetPassword) Content() mail.Content { return mail.View("mail/reset_password", m) }
