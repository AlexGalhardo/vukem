package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
)

// builtin holds the framework's own messages (lang/<locale>.json): the
// keyed errors of packages auth, social and orm.
//
//go:embed lang/*.json
var builtin embed.FS

// builtinCatalogs parses the embedded catalogs, flattened, by locale.
func builtinCatalogs() (map[string]map[string]message, error) {
	entries, err := fs.ReadDir(builtin, "lang")
	if err != nil {
		return nil, fmt.Errorf("i18n: read built-in catalogs: %w", err)
	}

	out := map[string]map[string]message{}

	for _, e := range entries {
		data, err := builtin.ReadFile(path.Join("lang", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("i18n: read built-in %s: %w", e.Name(), err)
		}

		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("i18n: parse built-in %s: %w", e.Name(), err)
		}

		flat := map[string]message{}
		if err := flatten("", raw, flat); err != nil {
			return nil, err
		}

		out[strings.TrimSuffix(e.Name(), ".json")] = flat
	}

	return out, nil
}

// AddBuiltin adds the framework's own messages, in English and Brazilian
// Portuguese, to the locales b serves: "auth.failed",
// "auth.unauthenticated", "auth.unverified", "auth.token_ability",
// "social.*", "validation.unique" and "validation.exists". Each built-in
// locale goes to the locale Match picks for it (pt-BR messages fill a
// "pt" catalog), so no locale is added that b would not serve already.
// Keys b already defines for that locale or its shorter forms are kept:
// the app's catalog always wins. vukem.WithTranslations calls it.
func (b *Bundle) AddBuiltin() error {
	cats, err := builtinCatalogs()
	if err != nil {
		return err
	}

	for locale, msgs := range cats {
		target, ok := b.Match(locale)
		if !ok {
			continue
		}

		b.addMissing(target, msgs)
	}

	return nil
}

// addMissing adds msgs to the catalog of locale, skipping keys found in
// locale or its shorter forms.
func (b *Bundle) addMissing(locale string, msgs map[string]message) {
	b.mu.Lock()
	defer b.mu.Unlock()

	chain := truncations(locale)

	for key, m := range msgs {
		defined := false

		for _, l := range chain {
			if _, ok := b.catalogs[l][key]; ok {
				defined = true
				break
			}
		}

		if defined {
			continue
		}

		if b.catalogs[locale] == nil {
			b.catalogs[locale] = map[string]message{}
		}

		b.catalogs[locale][key] = m
	}
}

// Translator returns an errs.Translator for locale, which translates the
// keyed errors of the framework (errs.NewKey, errs.Translate) along the
// same fallback chain as T. Unknown keys report false, without calling
// Options.OnMissing: the error keeps its English message. Middleware
// installs one for the resolved locale.
func (b *Bundle) Translator(locale string) errs.Translator {
	return func(key string, params map[string]any) (string, bool) {
		m, ok := b.find(locale, key)
		if !ok {
			return "", false
		}

		return interpolate(m.pick(locale, params["count"]), params), true
	}
}
