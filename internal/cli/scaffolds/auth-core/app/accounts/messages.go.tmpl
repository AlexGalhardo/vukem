package accounts

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/AlexGalhardo/vukem/i18n"
)

// lang/<locale>/*.json hold the "auth.*" messages; install:auth --2fa and
// --social add their own files next to auth.json.
//
//go:embed lang
var catalogs embed.FS

// loadMessages builds the bundle of the catalogs in lang/, with English as
// the fallback.
func loadMessages() (*i18n.Bundle, error) {
	b, err := i18n.New(i18n.Options{Default: "en"})
	if err != nil {
		return nil, err
	}

	err = fs.WalkDir(catalogs, "lang", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".json" {
			return err
		}

		data, err := catalogs.ReadFile(p)
		if err != nil {
			return err
		}

		var messages map[string]any
		if err := json.Unmarshal(data, &messages); err != nil {
			return fmt.Errorf("accounts: parse %s: %w", p, err)
		}

		return b.Add(path.Base(path.Dir(p)), messages)
	})
	if err != nil {
		return nil, fmt.Errorf("accounts: load messages: %w", err)
	}

	return b, nil
}

// T translates an "auth.*" message into the request locale, which
// i18n.Middleware resolves when the app uses vukem.WithTranslations
// (English otherwise). A key in the app's own catalog for that locale or
// its language (resources/lang/pt-BR.json, then pt.json) wins, so the app
// can reword a message or add a locale without editing lang/.
func (a *Accounts) T(ctx context.Context, key string, args ...any) string {
	locale := i18n.Locale(ctx)

	if app := i18n.BundleFrom(ctx); app != nil {
		if locale == "" {
			locale = app.Default()
		}

		// pt-BR, then pt: an app's pt.json serves pt-BR requests. The app's
		// default locale is not tried, so it never hides a translation here.
		for l := locale; l != ""; {
			if app.Has(l, key) {
				return app.T(l, key, args...)
			}

			i := strings.LastIndex(l, "-")
			if i < 0 {
				break
			}

			l = l[:i]
		}
	}

	if locale == "" {
		locale = a.Messages.Default()
	}

	return a.Messages.T(locale, key, args...)
}
