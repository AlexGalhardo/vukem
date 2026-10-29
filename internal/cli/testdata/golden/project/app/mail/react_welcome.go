package mail

import "github.com/AlexGalhardo/vukem/mail"

// ReactWelcome is an email; send it with app.Mailer() then Send(ctx, ReactWelcome{...}).
type ReactWelcome struct {
	To   string
	Name string
	URL  string
}

// Envelope implements mail.Mailable.
func (m ReactWelcome) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{m.To}, Subject: "ReactWelcome"}
}

// Content implements mail.Mailable: the React Email component
// resources/js/emails/react_welcome.tsx, exported to resources/emails/react_welcome.html.
func (m ReactWelcome) Content() mail.Content { return mail.React("react_welcome", m) }
