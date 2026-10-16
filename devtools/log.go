package devtools

import (
	"context"
	"log/slog"
	"slices"
	"strings"
)

// LogHandler returns a slog.Handler that keeps records at Config.Level
// or above; tee it next to the app handler with slog.NewMultiHandler.
func (d *Devtools) LogHandler() slog.Handler { return &logHandler{d: d} }

type logHandler struct {
	d      *Devtools
	pre    []string // "key=value" from WithAttrs, already qualified
	groups []string
}

func (h *logHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.d.cfg.Level.Level() }

func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	id, ok := traceID(ctx)
	if !ok {
		return nil
	}

	parts := slices.Clone(h.pre)
	prefix := groupPrefix(h.groups)

	r.Attrs(func(a slog.Attr) bool {
		parts = appendAttr(parts, prefix, a)
		return true
	})

	h.d.logs.push(Log{
		TraceID: id, Time: r.Time, Level: r.Level.String(), Message: r.Message, Attrs: strings.Join(parts, " "),
	})

	return nil
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.pre = slices.Clone(h.pre)

	prefix := groupPrefix(h.groups)
	for _, a := range attrs {
		c.pre = appendAttr(c.pre, prefix, a)
	}

	return &c
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	c := *h
	c.groups = append(slices.Clone(h.groups), name)

	return &c
}

func groupPrefix(groups []string) string {
	if len(groups) == 0 {
		return ""
	}

	return strings.Join(groups, ".") + "."
}

func appendAttr(parts []string, prefix string, a slog.Attr) []string {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return parts
	}

	if a.Value.Kind() == slog.KindGroup {
		p := prefix
		if a.Key != "" {
			p += a.Key + "."
		}

		for _, g := range a.Value.Group() {
			parts = appendAttr(parts, p, g)
		}

		return parts
	}

	return append(parts, prefix+a.Key+"="+a.Value.String())
}
