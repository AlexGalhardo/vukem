package mail

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	netmail "net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/view"
)

type welcome struct{ Name, Email string }

func (w welcome) Envelope() Envelope {
	return Envelope{
		To:      []string{w.Name + " <" + w.Email + ">"},
		Subject: "Bem-vindo, " + w.Name + "!",
		Tags:    map[string]string{"kind": "welcome"},
	}
}

func (w welcome) Content() Content { return View("mail/welcome", w) }

type invoice struct{ welcome }

func (invoice) Content() Content {
	return HTML(`<p>Total: <b>R$ 10</b></p><p><a href="https://x.test/i/1">Pay</a></p><img src="cid:logo">`, "")
}

func (invoice) Attachments() []Attachment {
	return []Attachment{
		{Filename: "invoice.pdf", Data: []byte("%PDF-1.4 fake")},
		{Filename: "logo.png", Data: []byte{0x89, 'P', 'N', 'G'}, Inline: true, ContentID: "logo"},
	}
}

type raw struct {
	env     Envelope
	content Content
}

func (r raw) Envelope() Envelope { return r.env }
func (r raw) Content() Content   { return r.content }

func views(t *testing.T) *view.Engine {
	t.Helper()

	e, err := view.New(view.Config{FS: fstest.MapFS{
		"mail/welcome.html": {
			Data: []byte(`{{/* layout: none */}}<h1>Hi {{.Name}}</h1><p>Welcome &amp; enjoy</p>`),
		},
		"mail/welcome_text.html": {Data: []byte(`{{/* layout: none */}}Hi {{.Name}}, welcome!`)},
		"mail/plain.html":        {Data: []byte(`{{/* layout: none */}}<style>p{}</style><p>Line one<br>Line two</p>`)},
	}})
	if err != nil {
		t.Fatal(err)
	}

	return e
}

func TestRenderAndArray(t *testing.T) {
	t.Parallel()

	arr := NewArray()
	m := New(arr, "Vukem App <app@example.com>", views(t))

	if err := m.Send(t.Context(), welcome{Name: "Ana", Email: "ana@example.com"}); err != nil {
		t.Fatal(err)
	}

	sent := arr.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent = %d", len(sent))
	}

	got := sent[0]
	if got.From != `"Vukem App" <app@example.com>` || got.To[0] != `"Ana" <ana@example.com>` || got.ID == "" ||
		got.HTML != "<h1>Hi Ana</h1><p>Welcome &amp; enjoy</p>" || got.Text != "Hi Ana, welcome!" {
		t.Fatalf("message = %+v", got)
	}

	if len(arr.SentTo("ana@example.com")) != 1 || len(arr.SentTo("bob@example.com")) != 0 {
		t.Fatal("SentTo")
	}

	// Text is derived from HTML when there is no _text view.
	msg, err := m.Render(t.Context(), raw{Envelope{To: []string{"a@b.co"}}, View("mail/plain", nil)})
	if err != nil || msg.Text != "Line one\nLine two" {
		t.Fatalf("derived text = %q %v", msg.Text, err)
	}

	msg, err = m.Render(t.Context(), invoice{welcome{"Ana", "ana@example.com"}})
	if err != nil || msg.Text != "Total: R$ 10\nPay (https://x.test/i/1)" || len(msg.Attachments) != 2 {
		t.Fatalf("invoice text = %q %v", msg.Text, err)
	}

	arr.Reset()

	if len(arr.Sent()) != 0 {
		t.Fatal("Reset")
	}
}

func TestSecurity_A05_MailHeaderInjection(t *testing.T) {
	t.Parallel()

	m := New(NewArray(), "app@example.com", nil)

	for name, mailable := range map[string]Mailable{
		"to injection":      raw{Envelope{To: []string{"a@b.co\r\nBcc: victim@x.io"}}, Text("x")},
		"from injection":    raw{Envelope{From: "x@y.z\nBcc: v@x.io", To: []string{"a@b.co"}}, Text("x")},
		"subject injection": raw{Envelope{To: []string{"a@b.co"}, Subject: "Hi\r\nBcc: v@x.io"}, Text("x")},
		"no recipients":     raw{Envelope{Subject: "x"}, Text("x")},
		"bad cc":            raw{Envelope{To: []string{"a@b.co"}, Cc: []string{"not an address"}}, Text("x")},
	} {
		if err := m.Send(t.Context(), mailable); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	if _, err := Build(
		&Message{From: "a@b.co", To: []string{"c@d.co"}, Tags: map[string]string{"x": "y\r\nBcc: v"}},
	); err == nil {
		t.Error("tag with line break accepted")
	}

	if _, err := Build(&Message{
		From: "a@b.co", To: []string{"c@d.co"},
		Attachments: []Attachment{{Filename: "a", Inline: true, ContentID: "x>\r\nBcc: v"}},
	}); err == nil {
		t.Error("content id with line break accepted")
	}

	// A content type taken from an upload must not inject part headers.
	if _, err := Build(&Message{
		From: "a@b.co", To: []string{"c@d.co"},
		Attachments: []Attachment{{Filename: "a.txt", ContentType: "text/plain\r\nContent-Transfer-Encoding: 7bit"}},
	}); err == nil {
		t.Error("attachment content type with line break accepted")
	}

	// Without a view engine View contents fail instead of sending empty mail.
	if err := m.Send(t.Context(), raw{Envelope{To: []string{"a@b.co"}}, View("x", nil)}); err == nil {
		t.Error("view without engine accepted")
	}

	if err := New(
		NewArray(),
		"app@example.com",
		views(t),
	).Send(t.Context(), raw{Envelope{To: []string{"a@b.co"}}, View("missing", nil)}); err == nil {
		t.Error("missing view accepted")
	}
}

func TestBuildMIME(t *testing.T) {
	t.Parallel()

	m := New(NewArray(), "app@example.com", views(t))
	msg, err := m.Render(t.Context(), invoice{welcome{"Ana", "ana@example.com"}})
	if err != nil {
		t.Fatal(err)
	}

	msg.Bcc = []string{"secret@example.com"}
	msg.Cc = []string{"cc@example.com"}
	msg.ReplyTo = []string{"support@example.com"}
	msg.Subject = "Fatura de outubro ✓"

	b, err := Build(msg)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := netmail.ReadMessage(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}

	dec := new(mime.WordDecoder)
	subject, _ := dec.DecodeHeader(parsed.Header.Get("Subject"))

	if subject != "Fatura de outubro ✓" || parsed.Header.Get("Bcc") != "" ||
		bytes.Contains(b, []byte("secret@example.com")) ||
		parsed.Header.Get("Cc") != "cc@example.com" ||
		parsed.Header.Get("X-Tag-Kind") != "welcome" ||
		!strings.HasSuffix(parsed.Header.Get("Message-Id"), "@example.com>") {
		t.Fatalf("headers = %v", parsed.Header)
	}

	// mixed → related(alternative(text, html), inline logo) + attachment
	parts := walk(t, parsed.Header.Get("Content-Type"), parsed.Body)

	want := []string{"text/plain; charset=utf-8", "text/html; charset=utf-8", "image/png", "application/pdf"}
	if strings.Join(parts.types, ",") != strings.Join(want, ",") {
		t.Fatalf("parts = %v", parts.types)
	}

	if parts.bodies["application/pdf"] != "%PDF-1.4 fake" ||
		!strings.Contains(parts.bodies["text/html; charset=utf-8"], "cid:logo") {
		t.Fatalf("bodies = %v", parts.bodies)
	}
}

type mimeParts struct {
	types  []string
	bodies map[string]string
}

func walk(t *testing.T, contentType string, r io.Reader) mimeParts {
	t.Helper()

	out := mimeParts{bodies: map[string]string{}}

	var rec func(ct string, r io.Reader)

	rec = func(ct string, r io.Reader) {
		mt, params, err := mime.ParseMediaType(ct)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.HasPrefix(mt, "multipart/") {
			return
		}

		mr := multipart.NewReader(r, params["boundary"])

		for {
			p, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				return
			}

			if err != nil {
				t.Fatal(err)
			}

			pct := p.Header.Get("Content-Type")
			if strings.HasPrefix(pct, "multipart/") {
				rec(pct, p)
				continue
			}

			data, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
			out.types = append(out.types, pct)
			out.bodies[pct] = string(data)
		}
	}

	rec(contentType, r)

	return out
}

// fakeSMTP is a minimal SMTP server with optional STARTTLS and AUTH PLAIN.
type fakeSMTP struct {
	ln       net.Listener
	tls      *tls.Config
	starttls bool

	mu   sync.Mutex
	auth string
	from string
	rcpt []string
	data string
}

func newFakeSMTP(t *testing.T, starttls bool) (*fakeSMTP, *x509.CertPool) {
	t.Helper()

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	f := &fakeSMTP{
		ln:       ln,
		starttls: starttls,
		tls: &tls.Config{
			Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
			MinVersion:   tls.VersionTLS12,
		},
	}

	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })

	return f, pool
}

func (f *fakeSMTP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}

		go f.session(conn)
	}
}

func (f *fakeSMTP) session(conn net.Conn) {
	defer conn.Close()

	r, w := bufio.NewReader(conn), bufio.NewWriter(conn)
	reply := func(s string) { _, _ = w.WriteString(s + "\r\n"); _ = w.Flush() }
	secure := false

	reply("220 localhost ESMTP fake")

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}

		cmd := strings.TrimSpace(line)

		switch up := strings.ToUpper(cmd); {
		case strings.HasPrefix(up, "EHLO"):
			if f.starttls && !secure {
				reply("250-localhost\r\n250-STARTTLS\r\n250 AUTH PLAIN")
			} else {
				reply("250-localhost\r\n250 AUTH PLAIN")
			}
		case up == "STARTTLS":
			reply("220 go ahead")

			tc := tls.Server(conn, f.tls)
			if tc.Handshake() != nil {
				return
			}

			conn, secure = tc, true
			r, w = bufio.NewReader(tc), bufio.NewWriter(tc)
		case strings.HasPrefix(up, "AUTH PLAIN"):
			f.mu.Lock()
			f.auth = strings.TrimSpace(cmd[len("AUTH PLAIN"):])
			f.mu.Unlock()
			reply("235 ok")
		case strings.HasPrefix(up, "MAIL FROM:"):
			f.mu.Lock()
			f.from = cmd[len("MAIL FROM:"):]
			f.mu.Unlock()
			reply("250 ok")
		case strings.HasPrefix(up, "RCPT TO:"):
			f.mu.Lock()
			f.rcpt = append(f.rcpt, cmd[len("RCPT TO:"):])
			f.mu.Unlock()
			reply("250 ok")
		case up == "DATA":
			reply("354 send")

			var b strings.Builder

			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}

				b.WriteString(l)
			}

			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			reply("250 queued")
		case up == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func TestSMTPTransport(t *testing.T) {
	t.Parallel()

	f, pool := newFakeSMTP(t, true)
	port := f.ln.Addr().(*net.TCPAddr).Port

	s := NewSMTP(SMTPConfig{
		Host: "localhost", Port: port, Username: "user", Password: config.NewSecret("pass"),
		TLSConfig: &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12},
	})

	// Connect to 127.0.0.1 while verifying the certificate for "localhost".
	s.cfg.Host = "localhost"

	m := New(s, "app@example.com", views(t))
	msg := welcome{Name: "Ana", Email: "ana@example.com"}

	if err := m.Send(t.Context(), mailWithBcc{msg}); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	creds, _ := base64.StdEncoding.DecodeString(f.auth)
	if string(creds) != "\x00user\x00pass" || f.from != "<app@example.com>" || len(f.rcpt) != 2 ||
		!strings.Contains(
			f.data,
			"Subject: Bem-vindo, Ana!\r\n",
		) || strings.Contains(f.data, "hidden@example.com") {
		t.Fatalf("server saw auth=%q from=%q rcpt=%v data=%q", creds, f.from, f.rcpt, f.data)
	}
}

type mailWithBcc struct{ welcome }

func (m mailWithBcc) Envelope() Envelope {
	e := m.welcome.Envelope()
	e.Bcc = []string{"hidden@example.com"}

	return e
}

func TestSMTPRequiresTLS(t *testing.T) {
	t.Parallel()

	f, _ := newFakeSMTP(t, false)
	port := f.ln.Addr().(*net.TCPAddr).Port
	msg := raw{Envelope{To: []string{"a@b.co"}}, Text("hi")}

	strict := New(NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: port}), "app@example.com", nil)
	if err := strict.Send(t.Context(), msg); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("plain server = %v, want a STARTTLS error", err)
	}

	local := New(NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: port, InsecureSkipTLS: true}), "app@example.com", nil)
	if err := local.Send(t.Context(), msg); err != nil {
		t.Fatalf("InsecureSkipTLS = %v", err)
	}

	down := New(NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: 1, Timeout: time.Second}), "app@example.com", nil)
	if err := down.Send(t.Context(), msg); err == nil {
		t.Fatal("dial to a closed port succeeded")
	}
}

func TestResendTransport(t *testing.T) {
	t.Parallel()

	var (
		calls atomic.Int32
		got   resendEmail
		keys  []string
		mu    sync.Mutex
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)

		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()

		if r.Header.Get("Authorization") != "Bearer re_secret_key" || r.URL.Path != "/emails" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch n {
		case 1:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"name":"rate_limit_exceeded","message":"Too many requests"}`))
		case 2:
			w.WriteHeader(http.StatusBadGateway)
		default:
			mu.Lock()
			_ = json.NewDecoder(r.Body).Decode(&got)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"id":"email_1"}`))
		}
	}))
	defer srv.Close()

	key := config.NewSecret("re_secret_key")
	tr := NewResend(ResendConfig{APIKey: key, BaseURL: srv.URL, MaxBackoff: 10 * time.Millisecond})

	m := New(tr, "app@example.com", views(t))
	if err := m.Send(t.Context(), invoice{welcome{"Ana", "ana@example.com"}}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if calls.Load() != 3 || keys[0] == "" || keys[0] != keys[2] {
		t.Fatalf("calls = %d keys = %v (retries must reuse the idempotency key)", calls.Load(), keys)
	}

	if got.From != "<app@example.com>" || got.Subject != "Bem-vindo, Ana!" || len(got.Attachments) != 2 ||
		got.Attachments[1].ContentID != "logo" || len(got.Tags) != 1 || got.Tags[0].Name != "kind" {
		t.Fatalf("payload = %+v", got)
	}
}

func TestSecurity_A09_ResendKeyRedacted(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"name":"validation_error","message":"The from address is invalid"}`))
	}))
	defer srv.Close()

	key := config.NewSecret("re_live_SUPERSECRET")
	cfg := ResendConfig{APIKey: key, BaseURL: srv.URL}

	var logs bytes.Buffer

	log := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	log.Info("config", "resend", cfg)

	err := New(
		NewResend(cfg),
		"app@example.com",
		nil,
	).Send(t.Context(), raw{Envelope{To: []string{"a@b.co"}}, Text("hi")})

	var re *ResendError
	if !errors.As(err, &re) || re.Status != 422 || re.Retryable() {
		t.Fatalf("err = %v", err)
	}

	jsonKey, jerr := json.Marshal(key)
	if jerr != nil {
		t.Fatal(jerr)
	}

	for name, out := range map[string]string{"error": err.Error(), "log": logs.String(), "json": string(jsonKey)} {
		if strings.Contains(out, "SUPERSECRET") {
			t.Errorf("API key leaked in %s: %s", name, out)
		}
	}

	if _, err := (&Resend{cfg: ResendConfig{BaseURL: "http://127.0.0.1:1", HTTP: &http.Client{Timeout: time.Second}}}).post(
		t.Context(),
		"id",
		nil,
	); err == nil {
		t.Fatal("request to a closed port succeeded")
	}
}

func TestLogTransportAndPreview(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	m := New(Log{L: slog.New(slog.NewTextHandler(&logs, nil))}, "app@example.com", views(t))
	if err := m.Send(t.Context(), welcome{Name: "Ana", Email: "ana@example.com"}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(logs.String(), "Bem-vindo") || strings.Contains(logs.String(), "Hi Ana") {
		t.Fatalf("log transport = %q (bodies only at debug)", logs.String())
	}

	if err := (Log{}).Send(t.Context(), &Message{}); err != nil {
		t.Fatal(err)
	}

	h := Preview(m, map[string]Mailable{
		"welcome": welcome{Name: "Ana", Email: "ana@example.com"},
		"broken":  raw{Envelope{}, Text("x")},
	})

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))

		return rec
	}

	if rec := get("/"); !strings.Contains(rec.Body.String(), `href="welcome"`) {
		t.Fatalf("index = %s", rec.Body.String())
	}

	if rec := get("/welcome"); rec.Body.String() != "<h1>Hi Ana</h1><p>Welcome &amp; enjoy</p>" {
		t.Fatalf("html = %s", rec.Body.String())
	}

	if rec := get("/welcome?text=1"); rec.Body.String() != "Hi Ana, welcome!" {
		t.Fatalf("text = %s", rec.Body.String())
	}

	if get("/nope").Code != 404 || get("/broken").Code != 500 {
		t.Fatal("preview errors")
	}
}
