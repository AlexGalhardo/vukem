package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/AlexGalhardo/vukem/config"
)

// ResendConfig configures the Resend transport (https://resend.com).
type ResendConfig struct {
	APIKey config.Secret
	// BaseURL defaults to https://api.resend.com (tests use a fake).
	BaseURL string
	// HTTP is the client (10s timeout by default).
	HTTP *http.Client
	// MaxRetries for 429 and 5xx responses (default 2).
	MaxRetries int
	// MaxBackoff caps waits between retries, including Retry-After (default 5s).
	MaxBackoff time.Duration
}

// Resend sends through the Resend HTTP API. The message ID is the
// Idempotency-Key, so retries never send twice.
type Resend struct {
	cfg ResendConfig
}

// NewResend returns the Resend transport.
func NewResend(cfg ResendConfig) *Resend {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.resend.com"
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

	return &Resend{cfg: cfg}
}

// ResendError is an error answered by the Resend API. It never contains
// the API key.
type ResendError struct {
	Status  int
	Name    string
	Message string
}

// Error returns the status, Resend error name and message.
func (e *ResendError) Error() string {
	return fmt.Sprintf("mail: resend %d %s: %s", e.Status, e.Name, e.Message)
}

// Retryable reports whether the request may succeed later.
func (e *ResendError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

type resendAttachment struct {
	Filename    string `json:"filename"`
	Content     string `json:"content"`
	ContentType string `json:"content_type,omitempty"`
	ContentID   string `json:"content_id,omitempty"`
}

type resendTag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type resendEmail struct {
	From        string             `json:"from"`
	To          []string           `json:"to"`
	Cc          []string           `json:"cc,omitempty"`
	Bcc         []string           `json:"bcc,omitempty"`
	ReplyTo     []string           `json:"reply_to,omitempty"`
	Subject     string             `json:"subject"`
	HTML        string             `json:"html,omitempty"`
	Text        string             `json:"text,omitempty"`
	Attachments []resendAttachment `json:"attachments,omitempty"`
	Tags        []resendTag        `json:"tags,omitempty"`
}

// Send implements Transport, retrying 429 and 5xx with backoff.
func (r *Resend) Send(ctx context.Context, msg *Message) error {
	body, err := json.Marshal(r.payload(msg))
	if err != nil {
		return err
	}

	var last error

	for attempt := 0; attempt <= r.cfg.MaxRetries; attempt++ {
		wait, err := r.post(ctx, msg.ID, body)
		if err == nil {
			return nil
		}

		last = err

		re, ok := err.(*ResendError) //nolint:errorlint // post returns *ResendError unwrapped
		if !ok || !re.Retryable() || attempt == r.cfg.MaxRetries {
			return err
		}

		if wait <= 0 {
			wait = time.Duration(200<<attempt) * time.Millisecond
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(wait, r.cfg.MaxBackoff)):
		}
	}

	return last
}

func (r *Resend) payload(msg *Message) resendEmail {
	p := resendEmail{
		From: msg.From, To: msg.To, Cc: msg.Cc, Bcc: msg.Bcc, ReplyTo: msg.ReplyTo,
		Subject: msg.Subject, HTML: msg.HTML, Text: msg.Text,
	}

	for _, a := range msg.Attachments {
		p.Attachments = append(p.Attachments, resendAttachment{
			Filename: a.Filename, Content: base64.StdEncoding.EncodeToString(a.Data),
			ContentType: a.ContentType, ContentID: a.ContentID,
		})
	}

	for k, v := range msg.Tags {
		p.Tags = append(p.Tags, resendTag{Name: k, Value: v})
	}

	return p
}

// post sends one request and returns the Retry-After wait for 429s.
func (r *Resend) post(ctx context.Context, id string, body []byte) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.BaseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}

	req.Header.Set("Authorization", "Bearer "+r.cfg.APIKey.Reveal())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", id)

	resp, err := r.cfg.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("mail: resend request: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return 0, nil
	}

	var apiErr struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}

	_ = json.Unmarshal(data, &apiErr)

	wait := time.Duration(0)
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		wait = time.Duration(s) * time.Second
	}

	return wait, &ResendError{Status: resp.StatusCode, Name: apiErr.Name, Message: apiErr.Message}
}
