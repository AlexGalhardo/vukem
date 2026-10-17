package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	netmail "net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
)

// mailtrapUserAgent is sent on every request: Mailtrap's edge protection
// may block requests without a normal client User-Agent.
const mailtrapUserAgent = "vukem-mail"

// MailtrapConfig configures the Mailtrap Email API transport
// (https://docs.mailtrap.io/developers/email-sending).
type MailtrapConfig struct {
	APIToken config.Secret
	// InboxID > 0 sends to that Email Sandbox inbox
	// (sandbox.api.mailtrap.io/api/send/{id}) instead of delivering.
	InboxID int64
	// BaseURL defaults to https://send.api.mailtrap.io, or
	// https://sandbox.api.mailtrap.io with InboxID (tests use a fake).
	BaseURL string
	// HTTP is the client (10s timeout by default).
	HTTP *http.Client
	// MaxRetries for 429 and 5xx responses (default 2).
	MaxRetries int
	// MaxBackoff caps waits between retries, including Retry-After (default 5s).
	MaxBackoff time.Duration
}

// Mailtrap sends through the Mailtrap Email API over HTTPS, for hosts
// that block outbound SMTP. The API has no idempotency key, so a retried
// 5xx answered after delivery may send twice.
type Mailtrap struct {
	cfg MailtrapConfig
}

// NewMailtrap returns the Mailtrap transport. Send fails without an API
// token.
func NewMailtrap(cfg MailtrapConfig) *Mailtrap {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://send.api.mailtrap.io"
		if cfg.InboxID > 0 {
			cfg.BaseURL = "https://sandbox.api.mailtrap.io"
		}
	}

	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 10 * time.Second}
	}

	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 2
	}

	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 5 * time.Second
	}

	return &Mailtrap{cfg: cfg}
}

// MailtrapError is an error answered by the Mailtrap API. It never
// contains the API token or the message body.
type MailtrapError struct {
	Status int
	Errors []string
}

// Error returns the status and the errors Mailtrap listed.
func (e *MailtrapError) Error() string {
	if len(e.Errors) == 0 {
		return fmt.Sprintf("mail: mailtrap %d %s", e.Status, http.StatusText(e.Status))
	}

	return fmt.Sprintf("mail: mailtrap %d: %s", e.Status, strings.Join(e.Errors, "; "))
}

// Retryable reports whether the request may succeed later.
func (e *MailtrapError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

type mailtrapAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type mailtrapAttachment struct {
	Content     string `json:"content"`
	Filename    string `json:"filename"`
	Type        string `json:"type,omitempty"`
	Disposition string `json:"disposition"`
	ContentID   string `json:"content_id,omitempty"`
}

type mailtrapEmail struct {
	From            mailtrapAddress      `json:"from"`
	To              []mailtrapAddress    `json:"to,omitempty"`
	Cc              []mailtrapAddress    `json:"cc,omitempty"`
	Bcc             []mailtrapAddress    `json:"bcc,omitempty"`
	ReplyTo         *mailtrapAddress     `json:"reply_to,omitempty"`
	Subject         string               `json:"subject"`
	Text            string               `json:"text,omitempty"`
	HTML            string               `json:"html,omitempty"`
	Attachments     []mailtrapAttachment `json:"attachments,omitempty"`
	Category        string               `json:"category,omitempty"`
	CustomVariables map[string]string    `json:"custom_variables,omitempty"`
}

// Send implements Transport, retrying 429 and 5xx with backoff.
func (m *Mailtrap) Send(ctx context.Context, msg *Message) error {
	if m.cfg.APIToken.IsZero() {
		return errors.New("mail: mailtrap needs an API token")
	}

	if m.cfg.InboxID < 0 {
		return fmt.Errorf("mail: mailtrap inbox id %d is negative", m.cfg.InboxID)
	}

	p, err := mailtrapPayload(msg)
	if err != nil {
		return err
	}

	body, err := json.Marshal(p)
	if err != nil {
		return err
	}

	var last error

	for attempt := 0; attempt <= m.cfg.MaxRetries; attempt++ {
		wait, err := m.post(ctx, body)
		if err == nil {
			return nil
		}

		last = err

		me, ok := err.(*MailtrapError) //nolint:errorlint // post returns *MailtrapError unwrapped
		if !ok || !me.Retryable() || attempt == m.cfg.MaxRetries {
			return err
		}

		if wait <= 0 {
			wait = time.Duration(200<<attempt) * time.Millisecond
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(wait, m.cfg.MaxBackoff)):
		}
	}

	return last
}

func (m *Mailtrap) endpoint() string {
	if m.cfg.InboxID > 0 {
		return m.cfg.BaseURL + "/api/send/" + strconv.FormatInt(m.cfg.InboxID, 10)
	}

	return m.cfg.BaseURL + "/api/send"
}

func mailtrapAddr(s string) (mailtrapAddress, error) {
	a, err := netmail.ParseAddress(s)
	if err != nil {
		return mailtrapAddress{}, fmt.Errorf("mail: mailtrap address: %w", err)
	}

	return mailtrapAddress{Email: a.Address, Name: a.Name}, nil
}

func mailtrapAddrs(list []string) ([]mailtrapAddress, error) {
	var out []mailtrapAddress

	for _, s := range list {
		a, err := mailtrapAddr(s)
		if err != nil {
			return nil, err
		}

		out = append(out, a)
	}

	return out, nil
}

func mailtrapPayload(msg *Message) (mailtrapEmail, error) {
	p := mailtrapEmail{Subject: msg.Subject, Text: msg.Text, HTML: msg.HTML}

	var err error

	if p.From, err = mailtrapAddr(msg.From); err != nil {
		return p, err
	}

	if p.To, err = mailtrapAddrs(msg.To); err != nil {
		return p, err
	}

	if p.Cc, err = mailtrapAddrs(msg.Cc); err != nil {
		return p, err
	}

	if p.Bcc, err = mailtrapAddrs(msg.Bcc); err != nil {
		return p, err
	}

	switch len(msg.ReplyTo) {
	case 0:
	case 1:
		a, err := mailtrapAddr(msg.ReplyTo[0])
		if err != nil {
			return p, err
		}

		p.ReplyTo = &a
	default:
		// Dropping the others silently would misroute replies.
		return p, errors.New("mail: mailtrap accepts a single reply-to address")
	}

	for _, a := range msg.Attachments {
		att := mailtrapAttachment{
			Content: base64.StdEncoding.EncodeToString(a.Data), Filename: a.Filename,
			Type: a.ContentType, Disposition: "attachment",
		}

		if a.Inline {
			att.Disposition, att.ContentID = "inline", a.ContentID
		}

		p.Attachments = append(p.Attachments, att)
	}

	for k, v := range msg.Tags {
		if k == "category" {
			p.Category = v
			continue
		}

		if p.CustomVariables == nil {
			p.CustomVariables = map[string]string{}
		}

		p.CustomVariables[k] = v
	}

	return p, nil
}

// post sends one request and returns the Retry-After wait for 429s.
func (m *Mailtrap) post(ctx context.Context, body []byte) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint(), bytes.NewReader(body))
	if err != nil {
		return 0, err
	}

	token := m.cfg.APIToken.Reveal()

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", mailtrapUserAgent)

	resp, err := m.cfg.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("mail: mailtrap request: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return 0, nil
	}

	var apiErr struct {
		Errors []string `json:"errors"`
	}

	_ = json.Unmarshal(data, &apiErr)

	// Upstream text is untrusted: never let it echo the token back.
	for i, e := range apiErr.Errors {
		apiErr.Errors[i] = strings.ReplaceAll(e, token, "[redacted]")
	}

	wait := time.Duration(0)
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		wait = time.Duration(s) * time.Second
	}

	return wait, &MailtrapError{Status: resp.StatusCode, Errors: apiErr.Errors}
}
