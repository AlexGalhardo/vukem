package view

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"maps"
	"slices"
	"strings"

	"github.com/AlexGalhardo/vukem/csrf"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/web"
)

// funcs returns the template functions bound to the request in ctx.
func (e *Engine) funcs(ctx context.Context) template.FuncMap {
	fm := template.FuncMap{
		"route": func(name string, params ...any) (string, error) {
			if e.cfg.URLs == nil {
				return "", errors.New("view: route needs Config.URLs")
			}

			return e.cfg.URLs.URL(name, params...)
		},
		"asset": func(p string) string {
			return strings.TrimSuffix(e.cfg.AssetPrefix, "/") + "/" + strings.TrimPrefix(p, "/")
		},
		"csrfToken": func() string { return csrf.Token(ctx) },
		"csrfField": func() template.HTML {
			t := template.HTMLEscapeString(csrf.Token(ctx))
			return trustedHTML(`<input type="hidden" name="` + csrf.FieldName + `" value="` + t + `">`)
		},
		"cspNonce": func() string { return middleware.CSPNonce(ctx) },
		"old":      func(field string) string { return oldInput(ctx, field) },
		"errors": func() map[string][]string {
			if s := web.SessionFrom(ctx); s != nil {
				return fieldErrors(s.Get(web.ErrorsKey))
			}

			return nil
		},
		"error": func(field string) string {
			if s := web.SessionFrom(ctx); s != nil {
				if msgs := fieldErrors(s.Get(web.ErrorsKey))[field]; len(msgs) > 0 {
					return msgs[0]
				}
			}

			return ""
		},
		"flash": func(key string) any {
			if s := web.SessionFrom(ctx); s != nil {
				return s.Get(key)
			}

			return nil
		},
		"json": func(v any) (template.JS, error) {
			b, err := json.Marshal(v)  // escapes <, >, & for safe embedding
			return template.JS(b), err //nolint:gosec // json.Marshal output is safe JS
		},
		"dict": dict,
		"htmxScript": func(src string) template.HTML {
			nonce := middleware.CSPNonce(ctx)

			attr := ""
			if nonce != "" {
				attr = ` nonce="` + template.HTMLEscapeString(nonce) + `"`
			}

			return trustedHTML(`<script src="` + template.HTMLEscapeString(src) + `" defer` + attr + `></script>`)
		},
	}

	maps.Copy(fm, e.cfg.Funcs)

	return fm
}

// dict builds a map from key/value pairs, for passing data to partials:
// {{template "components/alert" dict "Type" "error" "Text" .Msg}}.
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, errors.New("view: dict needs key/value pairs")
	}

	m := make(map[string]any, len(pairs)/2)

	for pair := range slices.Chunk(pairs, 2) {
		k, ok := pair[0].(string)
		if !ok {
			return nil, fmt.Errorf("view: dict key %v is not a string", pair[0])
		}

		m[k] = pair[1]
	}

	return m, nil
}

func oldInput(ctx context.Context, field string) string {
	s := web.SessionFrom(ctx)
	if s == nil {
		return ""
	}

	cur := s.Get(web.OldInputKey)

	for part := range strings.SplitSeq(field, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}

		cur = m[part]
	}

	switch v := cur.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		if len(v) > 0 {
			return fmt.Sprint(v[0])
		}

		return ""
	default:
		return fmt.Sprint(v)
	}
}

func fieldErrors(v any) map[string][]string {
	switch e := v.(type) {
	case map[string][]string:
		return e
	case map[string]any:
		out := make(map[string][]string, len(e))

		for k, msgs := range e {
			if list, ok := msgs.([]any); ok {
				for _, m := range list {
					out[k] = append(out[k], fmt.Sprint(m))
				}
			}
		}

		return out
	default:
		return nil
	}
}

// trustedHTML marks markup built from escaped parts as safe. Every dynamic
// value passed in must already be escaped with template.HTMLEscapeString.
func trustedHTML(s string) template.HTML {
	return template.HTML(s) //nolint:gosec // G203: callers escape every dynamic part
}
