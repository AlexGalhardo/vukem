package mail

import (
	"context"
	"log/slog"
	"net/mail"
	"slices"
	"strings"
	"sync"
)

// Array keeps sent messages in memory (tests; vukemtest uses it).
type Array struct {
	mu   sync.Mutex
	sent []Message
}

// NewArray returns an empty Array transport.
func NewArray() *Array { return &Array{} }

// Send implements Transport.
func (a *Array) Send(_ context.Context, msg *Message) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.sent = append(a.sent, *msg)

	return nil
}

// Sent returns a copy of the sent messages.
func (a *Array) Sent() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.sent)
}

// SentTo returns the messages with addr among To, Cc or Bcc.
func (a *Array) SentTo(addr string) []Message {
	sent := a.Sent()

	var out []Message

	for i := range sent {
		m := &sent[i]
		if slices.ContainsFunc(slices.Concat(m.To, m.Cc, m.Bcc), func(s string) bool { return sameAddress(s, addr) }) {
			out = append(out, *m)
		}
	}

	return out
}

func sameAddress(full, addr string) bool {
	a, err := mail.ParseAddress(full)
	return err == nil && strings.EqualFold(a.Address, addr)
}

// Reset forgets sent messages.
func (a *Array) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.sent = nil
}

// Log writes messages to a logger instead of sending them (development).
// Bodies are logged at debug level only.
type Log struct {
	L *slog.Logger
}

// Send implements Transport.
func (l Log) Send(ctx context.Context, msg *Message) error {
	log := l.L
	if log == nil {
		log = slog.Default()
	}

	log.InfoContext(ctx, "mail (log transport)", "to", msg.To, "cc", msg.Cc, "subject", msg.Subject,
		"attachments", len(msg.Attachments))
	log.DebugContext(ctx, "mail body", "text", msg.Text)

	return nil
}
