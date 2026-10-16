package mail

import "github.com/AlexGalhardo/vukem/mail"

// Welcome is an email; send it with app.Mailer() then Send(ctx, Welcome{...}).
type Welcome struct {
	To string
}

// Envelope implements mail.Mailable.
func (m Welcome) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{m.To}, Subject: "Welcome"}
}

// Content implements mail.Mailable (resources/views/mail/welcome.html).
func (m Welcome) Content() mail.Content { return mail.View("mail/welcome", m) }
