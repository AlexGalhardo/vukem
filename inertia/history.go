package inertia

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

// Session flash keys used by the adapter.
const (
	flashKey        = "_inertia_flash"
	clearHistoryKey = "_inertia_clear_history"
)

// Precognition headers.
const (
	HeaderPrecognition             = "Precognition"
	HeaderPrecognitionValidateOnly = "Precognition-Validate-Only"
	HeaderPrecognitionSuccess      = "Precognition-Success"
)

// EncryptHistory turns history encryption on or off for the page rendered
// for the request carrying ctx, overriding Config.EncryptHistory.
func EncryptHistory(ctx context.Context, on bool) {
	if s := stateFrom(ctx); s != nil {
		s.encrypt = &on
	}
}

// ClearHistory makes the next rendered page (after a redirect, if any)
// rotate the client's history encryption key, e.g. on logout.
func ClearHistory(c *web.Ctx) {
	if s := c.Session(); s != nil {
		s.Flash(clearHistoryKey, true)
	}
}

// Flash adds one-time data to the next rendered page's "flash" object (kept
// out of the client's history state). It survives a redirect.
func Flash(c *web.Ctx, key string, value any) {
	s := c.Session()
	if s == nil {
		return
	}

	data := map[string]any{}
	if prev, ok := s.Get(flashKey).(map[string]any); ok {
		maps.Copy(data, prev)
	}

	data[key] = value
	s.Flash(flashKey, data)
}

// IsPrefetch reports whether r is an Inertia prefetch request.
func IsPrefetch(r *http.Request) bool { return r.Header.Get("Purpose") == "prefetch" }

// pageFlags fills the session-driven page fields. Prefetch requests read
// flash data without consuming it, so the real visit still shows it.
func pageFlags(c *web.Ctx, st *state, p *Page) {
	p.EncryptHistory = st.in.cfg.EncryptHistory
	if st.encrypt != nil {
		p.EncryptHistory = *st.encrypt
	}

	s := c.Session()
	if s == nil {
		return
	}

	take := s.Pull
	if IsPrefetch(c.Request()) {
		take = s.Get
	}

	p.PreserveFragment = take(preserveFragmentKey) == true
	p.ClearHistory = take(clearHistoryKey) == true

	if f, ok := take(flashKey).(map[string]any); ok && len(f) > 0 {
		p.Flash = f
	}
}

// reflash keeps flash data across a redirect that renders no page.
func reflash(r *http.Request) {
	s := web.SessionFrom(r.Context())
	if s == nil {
		return
	}

	for _, k := range []string{flashKey, clearHistoryKey, preserveFragmentKey} {
		if v := s.Get(k); v != nil {
			s.Flash(k, v)
		}
	}
}

// Precognitive wraps h for Precognition (live validation): a request with
// "Precognition: true" only validates its input against schema and gets 204
// (valid) or 422 with the errors, filtered by Precognition-Validate-Only; h
// never runs for it. Other requests reach h, which parses the input itself.
func Precognitive[T any](schema z.Schema[T], h web.HandlerFunc) web.HandlerFunc {
	return func(c *web.Ctx) error {
		c.Writer().Header().Add("Vary", HeaderPrecognition)

		if c.Header(HeaderPrecognition) != "true" {
			return h(c)
		}

		c.SetHeader(HeaderPrecognition, "true")

		_, err := web.Parse(c, schema)

		verr, isValidation := errors.AsType[*errs.ValidationError](err)
		if err != nil && !isValidation {
			return err
		}

		fields := map[string][]string{}
		if verr != nil {
			fields = verr.Fields
		}

		if only := splitHeader(c.Header(HeaderPrecognitionValidateOnly)); only != nil {
			maps.DeleteFunc(fields, func(f string, _ []string) bool {
				return !slices.ContainsFunc(only, func(o string) bool { return f == o || strings.HasPrefix(f, o+".") })
			})
		}

		if len(fields) == 0 {
			c.SetHeader(HeaderPrecognitionSuccess, "true")
			return c.NoContent()
		}

		first := fields[slices.Sorted(maps.Keys(fields))[0]]

		return c.JSON(http.StatusUnprocessableEntity, map[string]any{"message": first[0], "errors": fields})
	}
}
