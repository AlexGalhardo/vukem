// Package logging holds log/slog handlers shared by Vukem apps.
//
// [Dedup] keeps a burst of identical errors from flooding logs and alerts:
// the first record of a kind is written at once, the repeats inside the
// window are counted, and one "repeated" record with the count closes the
// window.
//
//	d := logging.NewDedup(base, logging.DedupOptions{Window: time.Minute})
//	log := slog.New(d)
//	defer d.Close(ctx)
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#logging--observability.
package logging

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// DefaultDedupWindow is the window [NewDedup] uses when none is set.
const DefaultDedupWindow = time.Minute

// maxDedupKeys bounds the distinct records tracked per window; beyond it
// records pass through, since dropping logs is worse than repeating them.
const maxDedupKeys = 1024

// keyAttrs are the record attributes that, with the level and message,
// tell two records apart.
var keyAttrs = [...]string{"event", "error", "err"}

// DedupOptions configures [NewDedup].
type DedupOptions struct {
	// Window is how long repeats of a record are counted instead of
	// written (default [DefaultDedupWindow]).
	Window time.Duration
	// MinLevel is the lowest level deduplicated (default slog.LevelError);
	// lower records always pass through.
	MinLevel slog.Leveler
}

// Dedup is a slog.Handler that suppresses repeated identical records.
// Two records are identical when they share the level, the message, the
// handler's attributes and groups, and the values of the "event", "error"
// and "err" attributes.
//
// The first record of a window is passed to the next handler at once.
// When the window ends, if repeats were suppressed, a record with the
// message "repeated" carries "message" (the original message), "count"
// (the suppressed repeats) and "window". A repeat after the window starts
// a new one. Summaries are written by a timer per window, or by Close.
//
// Dedup is safe for concurrent use; handlers derived with WithAttrs and
// WithGroup share its state.
type Dedup struct {
	next   slog.Handler
	prefix string
	s      *dedupState
}

type dedupState struct {
	window time.Duration
	min    slog.Leveler

	mu      sync.Mutex
	closed  bool
	entries map[string]*dedupEntry
}

type dedupEntry struct {
	next    slog.Handler
	record  slog.Record
	count   int
	expires *time.Timer
}

// NewDedup returns a [Dedup] writing to next.
func NewDedup(next slog.Handler, opts DedupOptions) *Dedup {
	if opts.Window <= 0 {
		opts.Window = DefaultDedupWindow
	}

	if opts.MinLevel == nil {
		opts.MinLevel = slog.LevelError
	}

	return &Dedup{
		next: next,
		s: &dedupState{
			window:  opts.Window,
			min:     opts.MinLevel,
			entries: map[string]*dedupEntry{},
		},
	}
}

// Enabled reports whether the next handler handles level.
func (d *Dedup) Enabled(ctx context.Context, level slog.Level) bool {
	return d.next.Enabled(ctx, level)
}

// Handle writes r unless it repeats a record seen in the current window.
func (d *Dedup) Handle(ctx context.Context, r slog.Record) error {
	s := d.s
	if r.Level < s.min.Level() {
		return d.next.Handle(ctx, r)
	}

	key := d.key(&r)

	s.mu.Lock()

	if s.closed {
		s.mu.Unlock()
		return d.next.Handle(ctx, r)
	}

	if e, ok := s.entries[key]; ok {
		e.count++
		s.mu.Unlock()

		return nil
	}

	if len(s.entries) < maxDedupKeys {
		e := &dedupEntry{next: d.next, record: r.Clone()}
		e.expires = time.AfterFunc(s.window, func() { s.expire(key, e) })
		s.entries[key] = e
	}

	s.mu.Unlock()

	return d.next.Handle(ctx, r)
}

// WithAttrs returns a handler whose records carry attrs; it shares the
// window state with d.
func (d *Dedup) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return d
	}

	var b strings.Builder

	b.WriteString(d.prefix)

	for _, a := range attrs {
		b.WriteString(a.String())
		b.WriteByte(0)
	}

	return &Dedup{next: d.next.WithAttrs(attrs), prefix: b.String(), s: d.s}
}

// WithGroup returns a handler that nests later attributes under name; it
// shares the window state with d.
func (d *Dedup) WithGroup(name string) slog.Handler {
	if name == "" {
		return d
	}

	return &Dedup{next: d.next.WithGroup(name), prefix: d.prefix + "\x01" + name + "\x00", s: d.s}
}

// Close writes the pending "repeated" summaries; later records pass
// through unchanged. It is idempotent and returns the first error of the
// next handler, or ctx's error when ctx ends first.
func (d *Dedup) Close(ctx context.Context) error {
	s := d.s

	s.mu.Lock()
	s.closed = true
	pending := make([]*dedupEntry, 0, len(s.entries))

	for k, e := range s.entries {
		e.expires.Stop()
		delete(s.entries, k)
		pending = append(pending, e)
	}
	s.mu.Unlock()

	for _, e := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := s.summarize(ctx, e); err != nil {
			return err
		}
	}

	return nil
}

func (d *Dedup) key(r *slog.Record) string {
	vals := [len(keyAttrs)]string{}

	r.Attrs(func(a slog.Attr) bool {
		for i, k := range keyAttrs {
			if a.Key == k {
				vals[i] = a.Value.Resolve().String()
			}
		}

		return true
	})

	var b strings.Builder

	b.WriteString(r.Level.String())
	b.WriteByte(0)
	b.WriteString(d.prefix)
	b.WriteByte(0)
	b.WriteString(r.Message)

	for _, v := range vals {
		b.WriteByte(0)
		b.WriteString(v)
	}

	return b.String()
}

// expire ends e's window: called by its timer, a no-op when Close or a
// newer window already replaced it.
func (s *dedupState) expire(key string, e *dedupEntry) {
	s.mu.Lock()
	if s.entries[key] != e {
		s.mu.Unlock()
		return
	}

	delete(s.entries, key)
	s.mu.Unlock()

	// The timer has no caller to report to; the next handler owns its
	// own write errors (as it does for every record).
	_ = s.summarize(context.Background(), e)
}

func (s *dedupState) summarize(ctx context.Context, e *dedupEntry) error {
	if e.count == 0 {
		return nil
	}

	r := slog.NewRecord(time.Now(), e.record.Level, "repeated", 0)
	r.AddAttrs(
		slog.String("message", e.record.Message),
		slog.Int("count", e.count),
		slog.Duration("window", s.window),
	)

	e.record.Attrs(func(a slog.Attr) bool {
		for _, k := range keyAttrs {
			if a.Key == k {
				r.AddAttrs(a)
			}
		}

		return true
	})

	return e.next.Handle(ctx, r)
}
