package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
)

// SMTPConfig configures the SMTP transport.
type SMTPConfig struct {
	Host     string
	Port     int // 587 (STARTTLS, default) or 465 (implicit TLS)
	Username string
	Password config.Secret
	// InsecureSkipTLS sends without TLS (local catchers such as Mailpit
	// only); by default STARTTLS is required.
	InsecureSkipTLS bool
	// Timeout bounds dialing and the whole exchange (default 30s).
	Timeout time.Duration
	// TLSConfig overrides the TLS settings (tests).
	TLSConfig *tls.Config
}

// SMTP sends through an SMTP server with STARTTLS (or implicit TLS on 465).
type SMTP struct {
	cfg SMTPConfig
}

// NewSMTP returns the SMTP transport.
func NewSMTP(cfg SMTPConfig) *SMTP {
	if cfg.Port == 0 {
		cfg.Port = 587
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	return &SMTP{cfg: cfg}
}

// Send implements Transport.
func (s *SMTP) Send(ctx context.Context, msg *Message) error {
	body, err := Build(msg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	tlsCfg := s.cfg.TLSConfig

	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	}

	var conn net.Conn

	d := &net.Dialer{}
	if s.cfg.Port == 465 {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}

	if err != nil {
		return fmt.Errorf("mail: smtp dial: %w", err)
	}

	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mail: smtp: %w", err)
	}
	defer c.Close()

	if s.cfg.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("mail: smtp starttls: %w", err)
			}
		} else if !s.cfg.InsecureSkipTLS {
			return errors.New("mail: smtp server does not offer STARTTLS (set InsecureSkipTLS only for local catchers)")
		}
	}

	if s.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password.Reveal(), s.cfg.Host)); err != nil {
			return fmt.Errorf("mail: smtp auth: %w", err)
		}
	}

	from, _ := mail.ParseAddress(msg.From)
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("mail: smtp MAIL FROM: %w", err)
	}

	for _, list := range [][]string{msg.To, msg.Cc, msg.Bcc} {
		for _, r := range list {
			a, _ := mail.ParseAddress(r)
			if err := c.Rcpt(a.Address); err != nil {
				return fmt.Errorf("mail: smtp RCPT TO: %w", err)
			}
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: smtp DATA: %w", err)
	}

	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("mail: smtp write: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: smtp DATA end: %w", err)
	}

	return c.Quit()
}

// Build renders msg as RFC 5322 bytes: multipart/mixed (attachments) over
// multipart/related (inline parts) over multipart/alternative (text, HTML).
// Bcc is never written as a header.
func Build(msg *Message) ([]byte, error) {
	var b bytes.Buffer

	header := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }

	header("From", msg.From)
	header("To", strings.Join(msg.To, ", "))

	if len(msg.Cc) > 0 {
		header("Cc", strings.Join(msg.Cc, ", "))
	}

	if len(msg.ReplyTo) > 0 {
		header("Reply-To", strings.Join(msg.ReplyTo, ", "))
	}

	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Date", time.Now().UTC().Format(time.RFC1123Z))

	domain := "localhost"
	if a, err := mail.ParseAddress(msg.From); err == nil {
		if _, d, ok := strings.Cut(a.Address, "@"); ok {
			domain = d
		}
	}

	header("Message-ID", "<"+msg.ID+"@"+domain+">")
	header("MIME-Version", "1.0")

	for k, v := range msg.Tags {
		if strings.ContainsAny(k+v, "\r\n:") {
			return nil, errors.New("mail: tag contains a line break or colon")
		}

		header("X-Tag-"+k, v)
	}

	var inline, files []Attachment

	for _, a := range msg.Attachments {
		if a.Inline {
			inline = append(inline, a)
		} else {
			files = append(files, a)
		}
	}

	mixed := multipart.NewWriter(&b)
	header("Content-Type", `multipart/mixed; boundary="`+mixed.Boundary()+`"`)
	b.WriteString("\r\n")

	if err := writeBody(mixed, msg, inline); err != nil {
		return nil, err
	}

	for _, a := range files {
		if err := writeAttachment(mixed, a, "attachment"); err != nil {
			return nil, err
		}
	}

	if err := mixed.Close(); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}

func writeBody(mixed *multipart.Writer, msg *Message, inline []Attachment) error {
	var rel bytes.Buffer

	related := multipart.NewWriter(&rel)
	alt := &bytes.Buffer{}
	altW := multipart.NewWriter(alt)

	for _, part := range []struct{ ct, body string }{{"text/plain", msg.Text}, {"text/html", msg.HTML}} {
		if part.body == "" {
			continue
		}

		w, err := altW.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.ct + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return err
		}

		if err := writeBase64(w, []byte(part.body)); err != nil {
			return err
		}
	}

	if err := altW.Close(); err != nil {
		return err
	}

	w, err := related.CreatePart(
		textproto.MIMEHeader{"Content-Type": {`multipart/alternative; boundary="` + altW.Boundary() + `"`}},
	)
	if err != nil {
		return err
	}

	if _, err := w.Write(alt.Bytes()); err != nil {
		return err
	}

	for _, a := range inline {
		if err := writeAttachment(related, a, "inline"); err != nil {
			return err
		}
	}

	if err := related.Close(); err != nil {
		return err
	}

	part, err := mixed.CreatePart(
		textproto.MIMEHeader{"Content-Type": {`multipart/related; boundary="` + related.Boundary() + `"`}},
	)
	if err != nil {
		return err
	}

	_, err = part.Write(rel.Bytes())

	return err
}

func writeAttachment(mw *multipart.Writer, a Attachment, disposition string) error {
	ct := a.ContentType
	if ct == "" {
		ct = mime.TypeByExtension(fileExt(a.Filename))
		if ct == "" {
			ct = "application/octet-stream"
		}
	}

	// The type may come from an upload: parse and re-format it so it can
	// never carry a line break into the part headers.
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return fmt.Errorf("mail: invalid attachment content type: %w", err)
	}

	if ct = mime.FormatMediaType(mt, params); ct == "" {
		return errors.New("mail: invalid attachment content type")
	}

	h := textproto.MIMEHeader{
		"Content-Type":              {ct},
		"Content-Transfer-Encoding": {"base64"},
		"Content-Disposition":       {mime.FormatMediaType(disposition, map[string]string{"filename": a.Filename})},
	}

	if a.ContentID != "" {
		if strings.ContainsAny(a.ContentID, "\r\n<>") {
			return errors.New("mail: invalid content id")
		}

		h.Set("Content-ID", "<"+a.ContentID+">")
	}

	w, err := mw.CreatePart(h)
	if err != nil {
		return err
	}

	return writeBase64(w, a.Data)
}

func fileExt(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i:]
	}

	return ""
}

// writeBase64 writes base64 in 76-character lines (RFC 2045).
func writeBase64(w interface{ Write([]byte) (int, error) }, data []byte) error {
	enc := base64.StdEncoding.EncodeToString(data)

	for len(enc) > 76 {
		if _, err := w.Write([]byte(enc[:76] + "\r\n")); err != nil {
			return err
		}

		enc = enc[76:]
	}

	_, err := w.Write([]byte(enc + "\r\n"))

	return err
}
