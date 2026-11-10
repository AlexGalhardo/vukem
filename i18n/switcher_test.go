package i18n_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/i18n"
	"github.com/AlexGalhardo/vukem/web"
)

// switchLocale is the handler of the locale switcher in the docs: Inertia's
// router.post sends JSON, which FormValue ignores, so it reads the body
// with Input.
func switchLocale(b *i18n.Bundle) func(c *web.Ctx) error {
	return func(c *web.Ctx) error {
		in, err := c.Input()
		if err != nil {
			return err
		}

		loc, _ := in["locale"].(string)

		l, ok := b.Match(loc)
		if !ok {
			return errs.ErrNotFound
		}

		return c.String(http.StatusOK, l)
	}
}

func TestLocaleSwitcherReadsBody(t *testing.T) {
	t.Parallel()

	b, err := i18n.Load(fstest.MapFS{
		"en.json":    {Data: []byte(`{}`)},
		"pt-BR.json": {Data: []byte(`{}`)},
	}, i18n.Options{Default: "en"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, contentType, body string
		want                    string
		wantErr                 error
	}{
		{"json", "application/json", `{"locale":"pt-BR"}`, "pt-BR", nil},
		{"form", "application/x-www-form-urlencoded", "locale=pt-BR", "pt-BR", nil},
		{"non-string", "application/json", `{"locale":42}`, "", errs.ErrNotFound},
		{"missing", "application/json", `{}`, "", errs.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/locale", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)

			rec := httptest.NewRecorder()
			if err := switchLocale(b)(web.NewCtx(rec, req, nil)); !errors.Is(err, tt.wantErr) {
				t.Fatalf("POST /locale %q error = %v, want %v", tt.body, err, tt.wantErr)
			}

			if got := rec.Body.String(); got != tt.want {
				t.Errorf("POST /locale %q = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}
