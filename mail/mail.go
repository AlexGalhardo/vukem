// Package mail sends email. A Mailable describes one email (envelope and
// content); a Mailer renders it and hands the Message to a Transport:
// SMTP, Resend, log (development) or Array (tests).
//
//	type Welcome struct{ User models.User }
//
//	func (m Welcome) Envelope() mail.Envelope {
//		return mail.Envelope{To: []string{m.User.Email}, Subject: "Welcome!"}
//	}
//	func (m Welcome) Content() mail.Content { return mail.View("mail/welcome", m) }
//
//	err := app.Mailer().Send(ctx, Welcome{User: u})
//
// The app builds its [Mailer] from MAIL_* variables; [Preview] renders
// mailables in the browser during development.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#mail.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"regexp"
	"strings"

	"github.com/AlexGalhardo/vukem/web"
)

// Envelope holds addresses and headers. Addresses accept "Name <a@b.c>".
type Envelope struct {
	From    string
	To      []string
	Cc      []string
	Bcc     []string
	ReplyTo []string
	Subject string
	// Tags are provider metadata (Resend tags; X-Tag headers over SMTP;
	// Mailtrap category and custom variables). Names must not be empty and
	// neither may contain a line break or colon; Resend also limits them
	// to ASCII letters, digits, "_" and "-".
	Tags map[string]string
}

// Content is the body: a view (rendered by the Mailer) or literal parts.
type Content struct {
	view string
	data any
	html string
	text string
	// react renders view with the SetReact engine instead of the app views.
	react bool
}

// React renders the React Email template name: a page that `email export`
// wrote into the [vukem.WithReactEmails] directory, with props as its data.
// The exported HTML holds Go template actions where the component had
// props, so html/template escapes every prop for its context at send time
// and no Node process runs on send. The plain-text part is derived from
// the HTML, without the inbox preview text. A name with no exported page
// is an error.
//
// [vukem.WithReactEmails]: https://pkg.go.dev/github.com/AlexGalhardo/vukem#WithReactEmails
func React(name string, props any) Content {
	return Content{view: name, data: props, react: true}
}

// View renders the html/template page name with data; the plain-text part
// comes from the page "<name>_text" when it exists, else from the HTML.
func View(name string, data any) Content { return Content{view: name, data: data} }

// HTML is literal HTML (and optional text) content.
func HTML(htmlBody, text string) Content { return Content{html: htmlBody, text: text} }

// Text is plain-text content.
func Text(text string) Content { return Content{text: text} }

// Attachment is a file sent with the message; Inline parts are referenced
// from HTML as cid:<ContentID>.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
	Inline      bool
	ContentID   string
}

// Mailable is one email.
type Mailable interface {
	Envelope() Envelope
	Content() Content
}

// ContextEnveloper is implemented by mailables whose envelope depends on
// the send's context, such as a subject in the request locale:
//
//	func (m Welcome) EnvelopeContext(ctx context.Context) mail.Envelope {
//		return mail.Envelope{To: []string{m.User.Email}, Subject: i18n.T(ctx, "mail.welcome")}
//	}
//
// The Mailer calls it instead of Envelope, which stays the fallback for
// code without a context. SendLater renders before queueing, so the
// subject uses the context of the SendLater call.
type ContextEnveloper interface {
	EnvelopeContext(ctx context.Context) Envelope
}

// Attachable is implemented by mailables that attach files.
type Attachable interface {
	Attachments() []Attachment
}

// Message is a rendered email ready for a Transport.
type Message struct {
	// ID identifies the message (Message-ID, Resend idempotency key).
	ID          string
	From        string
	To          []string
	Cc          []string
	Bcc         []string
	ReplyTo     []string
	Subject     string
	HTML        string
	Text        string
	Attachments []Attachment
	Tags        map[string]string
}

// Transport delivers messages.
type Transport interface {
	Send(ctx context.Context, msg *Message) error
}

// Mailer renders mailables and sends them.
type Mailer struct {
	from      string
	transport Transport
	views     web.Renderer
	react     web.Renderer
	// Pointers keep Mailer comparable, as it was before SendLater.
	enqueue   *Enqueue
	enqueueTx *EnqueueTx
}

// New returns a Mailer sending from the default from address through t.
// views renders View contents (the app's view engine); it may be nil when
// only literal contents are used.
func New(t Transport, from string, views web.Renderer) *Mailer {
	return &Mailer{from: from, transport: t, views: views}
}

// SetReact sets the engine that renders [React] contents (the
// WithReactEmails directory). Call it before the mailer is used;
// app.Mailer sets it.
func (m *Mailer) SetReact(r web.Renderer) { m.react = r }

// Transport returns the mailer's transport (tests read the Array).
func (m *Mailer) Transport() Transport { return m.transport }

// Send renders and delivers mailable.
func (m *Mailer) Send(ctx context.Context, mailable Mailable) error {
	msg, err := m.Render(ctx, mailable)
	if err != nil {
		return err
	}

	return m.transport.Send(ctx, msg)
}

// ErrNoRecipients is returned for messages without To, Cc or Bcc.
var ErrNoRecipients = errors.New("mail: no recipients")

// Render builds the Message without sending it (previews, tests).
func (m *Mailer) Render(ctx context.Context, mailable Mailable) (*Message, error) {
	var env Envelope
	if ce, ok := mailable.(ContextEnveloper); ok {
		env = ce.EnvelopeContext(ctx)
	} else {
		env = mailable.Envelope()
	}

	msg := &Message{ID: rand.Text(), From: env.From, Subject: env.Subject, Tags: env.Tags}
	if msg.From == "" {
		msg.From = m.from
	}

	var err error

	if msg.From, err = address(msg.From); err != nil {
		return nil, fmt.Errorf("mail: from: %w", err)
	}

	for _, list := range []struct {
		in  []string
		out *[]string
	}{{env.To, &msg.To}, {env.Cc, &msg.Cc}, {env.Bcc, &msg.Bcc}, {env.ReplyTo, &msg.ReplyTo}} {
		for _, a := range list.in {
			addr, err := address(a)
			if err != nil {
				return nil, fmt.Errorf("mail: recipient: %w", err)
			}

			*list.out = append(*list.out, addr)
		}
	}

	if len(msg.To)+len(msg.Cc)+len(msg.Bcc) == 0 {
		return nil, ErrNoRecipients
	}

	if strings.ContainsAny(msg.Subject, "\r\n") {
		return nil, errors.New("mail: subject contains a line break")
	}

	if err := checkTags(msg.Tags); err != nil {
		return nil, err
	}

	if err := m.content(ctx, mailable.Content(), msg); err != nil {
		return nil, err
	}

	if wa, ok := mailable.(Attachable); ok {
		msg.Attachments = wa.Attachments()
	}

	return msg, nil
}

func (m *Mailer) content(ctx context.Context, c Content, msg *Message) error {
	if c.view == "" {
		msg.HTML, msg.Text = c.html, c.text
		if msg.Text == "" && msg.HTML != "" {
			msg.Text = textFromHTML(msg.HTML)
		}

		return nil
	}

	if c.react {
		return m.reactContent(ctx, c, msg)
	}

	if m.views == nil {
		return errors.New("mail: view content needs a view engine")
	}

	var b bytes.Buffer
	if err := m.views.Render(ctx, &b, c.view, c.data); err != nil {
		return fmt.Errorf("mail: render %s: %w", c.view, err)
	}

	msg.HTML = b.String()

	if text := c.view + "_text"; m.views.Has(text) {
		b.Reset()

		if err := m.views.Render(ctx, &b, text, c.data); err != nil {
			return fmt.Errorf("mail: render %s: %w", text, err)
		}

		msg.Text = b.String()
	} else {
		msg.Text = textFromHTML(msg.HTML)
	}

	return nil
}

func (m *Mailer) reactContent(ctx context.Context, c Content, msg *Message) error {
	if m.react == nil {
		return errors.New("mail: React content needs React email templates (vukem.WithReactEmails)")
	}

	if !m.react.Has(c.view) {
		return fmt.Errorf("mail: React email %q not found: run the emails build (email export)", c.view)
	}

	var b bytes.Buffer
	if err := m.react.Render(ctx, &b, c.view, c.data); err != nil {
		return fmt.Errorf("mail: render React email %s: %w", c.view, err)
	}

	msg.HTML = b.String()
	msg.Text = textFromHTML(msg.HTML)

	return nil
}

// checkTags applies the rule every transport needs (SMTP writes tags as
// X-Tag headers): a non-empty name, no line break or colon. Transports
// add their provider's rules (Resend).
func checkTags(tags map[string]string) error {
	for k, v := range tags {
		if k == "" {
			return errors.New("mail: tag with an empty name")
		}

		if strings.ContainsAny(k+v, "\r\n:") {
			return errors.New("mail: tag contains a line break or colon")
		}
	}

	return nil
}

// address parses and normalizes an address; parsing rejects line breaks,
// so headers cannot be injected through addresses.
func address(s string) (string, error) {
	a, err := mail.ParseAddress(s)
	if err != nil {
		return "", fmt.Errorf("%q: %w", s, err)
	}

	return a.String(), nil
}

var (
	reBlock = regexp.MustCompile(`(?is)<(script|style|head)[^>]*>.*?</(script|style|head)>`)
	// reSkip drops React Email's <Preview> inbox text: its text and filler
	// div hold no tags because html/template escaped them.
	reSkip  = regexp.MustCompile(`(?is)<div[^>]*data-skip-in-text="true"[^>]*>[^<]*<div>[^<]*</div>\s*</div>`)
	reBreak = regexp.MustCompile(`(?i)<(br|/p|/div|/h[1-6]|/li|/tr)\s*/?>`)
	reLink  = regexp.MustCompile(`(?is)<a\s[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	reTag   = regexp.MustCompile(`<[^>]+>`)
	reSpace = regexp.MustCompile(`[ \t]+`)
	reLines = regexp.MustCompile(`\n\s*\n\s*\n+`)
)

// textFromHTML derives a readable plain-text part (links become
// "text (url)").
func textFromHTML(h string) string {
	s := reBlock.ReplaceAllString(h, "")
	s = reSkip.ReplaceAllString(s, "")
	s = reLink.ReplaceAllString(s, "$2 ($1)")
	s = reBreak.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = reSpace.ReplaceAllString(s, " ")

	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}

	return strings.TrimSpace(reLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
