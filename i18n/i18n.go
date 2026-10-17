// Package i18n translates messages from JSON catalogs: one file per locale
// (resources/lang/en.json, pt-BR.json...), nested or dotted keys, {name}
// placeholders, CLDR plural categories, a locale fallback chain
// (pt-BR → pt → default), a locale-resolving middleware and translated
// validation messages.
//
//	{
//		"welcome": "Hello, {name}!",
//		"posts": {
//			"created": "Post created.",
//			"count": {"zero": "No posts", "one": "{count} post", "other": "{count} posts"}
//		}
//	}
//
//	b, err := i18n.Load(os.DirFS("resources/lang"), i18n.Options{Default: "en"})
//	b.T("pt-BR", "posts.count", "count", 3) // "3 posts" from pt-BR, pt or en
//
// A missing key returns the key itself (reported through Options.OnMissing);
// translating never panics.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#i18n.
package i18n

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/AlexGalhardo/vukem/z"
)

// DefaultLocale is the default locale when Options.Default is empty.
const DefaultLocale = "en"

// ValidationPrefix is the catalog namespace for validation messages:
// "validation.required", "validation.too_small.string" and
// "validation.attributes.<field>" (see z.Issues.Localize).
const ValidationPrefix = "validation."

var placeholder = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

// Options configures a Bundle.
type Options struct {
	// Default is the last locale of every fallback chain (default "en").
	Default string
	// OnMissing, when set, is called by T for keys found in no locale of
	// the chain (log or collect them; T still returns the key).
	OnMissing func(locale, key string)
}

// Bundle holds the catalogs of every locale. It is safe for concurrent use.
type Bundle struct {
	def       string
	onMissing func(locale, key string)

	mu       sync.RWMutex
	catalogs map[string]map[string]message
}

// message is a plain string or a set of plural forms keyed by category.
type message struct {
	text  string
	forms map[string]string
}

// New returns an empty Bundle; add catalogs with Add.
func New(opts Options) (*Bundle, error) {
	def := opts.Default
	if def == "" {
		def = DefaultLocale
	}

	canon, ok := Canonical(def)
	if !ok {
		return nil, fmt.Errorf("i18n: default %q: %w", def, ErrInvalidLocale)
	}

	return &Bundle{def: canon, onMissing: opts.OnMissing, catalogs: map[string]map[string]message{}}, nil
}

// ErrInvalidLocale reports a locale that is not a well-formed tag
// (language[-Script][-REGION], e.g. "en", "pt-BR", "zh-Hant-TW").
var ErrInvalidLocale = errors.New("invalid locale")

// Load reads every <locale>.json file at the root of fsys
// (os.DirFS("resources/lang") or an embed.FS) into a new Bundle. File names
// must be valid locales ("pt-BR.json", "pt_BR.json"); other files are
// ignored.
func Load(fsys fs.FS, opts Options) (*Bundle, error) {
	b, err := New(opts)
	if err != nil {
		return nil, err
	}

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("i18n: read catalogs: %w", err)
	}

	seen := map[string]string{}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || path.Ext(name) != ".json" {
			continue
		}

		locale, ok := Canonical(strings.TrimSuffix(name, ".json"))
		if !ok {
			return nil, fmt.Errorf("i18n: %s: %w", name, ErrInvalidLocale)
		}

		if prev, dup := seen[locale]; dup {
			return nil, fmt.Errorf("i18n: %s: duplicate catalog for %s (also %s)", name, locale, prev)
		}

		seen[locale] = name

		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("i18n: read %s: %w", name, err)
		}

		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("i18n: parse %s: %w", name, err)
		}

		if err := b.Add(locale, raw); err != nil {
			return nil, fmt.Errorf("i18n: %s: %w", name, err)
		}
	}

	return b, nil
}

// Add merges messages into the catalog of locale. Values are strings,
// nested maps (keys joined with ".") or plural maps: a map whose keys are
// all plural categories (zero, one, two, few, many, other) including
// "other".
func (b *Bundle) Add(locale string, messages map[string]any) error {
	canon, ok := Canonical(locale)
	if !ok {
		return fmt.Errorf("i18n: %q: %w", locale, ErrInvalidLocale)
	}

	flat := map[string]message{}
	if err := flatten("", messages, flat); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.catalogs[canon] == nil {
		b.catalogs[canon] = map[string]message{}
	}

	maps.Copy(b.catalogs[canon], flat)

	return nil
}

func flatten(prefix string, in map[string]any, out map[string]message) error {
	for k, v := range in {
		key := prefix + k

		switch v := v.(type) {
		case string:
			out[key] = message{text: v}
		case map[string]string:
			if !isPlural(v) {
				return fmt.Errorf("i18n: key %q: plural forms need \"other\" and CLDR category names", key)
			}

			out[key] = message{forms: maps.Clone(v)}
		case map[string]any:
			if forms, ok := pluralForms(v); ok {
				out[key] = message{forms: forms}
				continue
			}

			if err := flatten(key+".", v, out); err != nil {
				return err
			}
		default:
			return fmt.Errorf("i18n: key %q: unsupported value of type %T (want string or object)", key, v)
		}
	}

	return nil
}

func pluralForms(m map[string]any) (map[string]string, bool) {
	forms := make(map[string]string, len(m))

	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}

		forms[k] = s
	}

	return forms, isPlural(forms)
}

func isPlural(forms map[string]string) bool {
	if _, ok := forms[Other]; !ok {
		return false
	}

	for k := range forms {
		if !slices.Contains(categories, k) {
			return false
		}
	}

	return true
}

// Default returns the default locale.
func (b *Bundle) Default() string { return b.def }

// Locales lists the locales that have a catalog, sorted.
func (b *Bundle) Locales() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return slices.Sorted(maps.Keys(b.catalogs))
}

// Has reports whether key exists in the catalog of locale itself (no
// fallback).
func (b *Bundle) Has(locale, key string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	_, ok := b.catalogs[locale][key]

	return ok
}

// Match returns the available locale (one with a catalog, or the default)
// that best serves tag: the tag itself, then its shorter forms
// (pt-Hant-BR → pt-Hant → pt), then any locale of the same language
// (pt → pt-BR). Invalid tags never match.
func (b *Bundle) Match(tag string) (string, bool) {
	return b.match(tag, func(string) bool { return true })
}

// match is Match restricted to the locales allow accepts.
func (b *Bundle) match(tag string, allow func(string) bool) (string, bool) {
	canon, ok := Canonical(tag)
	if !ok {
		return "", false
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	available := func(l string) bool {
		_, ok := b.catalogs[l]
		return (ok || l == b.def) && allow(l)
	}

	for _, l := range truncations(canon) {
		if available(l) {
			return l, true
		}
	}

	lang := baseLanguage(canon)
	for _, l := range slices.Sorted(maps.Keys(b.catalogs)) {
		if baseLanguage(l) == lang && allow(l) {
			return l, true
		}
	}

	return "", false
}

// T translates key for locale, falling back along the chain locale →
// shorter forms → default locale. Args are key/value pairs ("name", "Ana")
// or a single map[string]any; they fill {name} placeholders. A numeric
// "count" argument selects the plural form (an explicit "zero" form wins
// for 0 in every language). A missing key returns key.
func (b *Bundle) T(locale, key string, args ...any) string {
	params := argMap(args)

	m, ok := b.find(locale, key)
	if !ok {
		if b.onMissing != nil {
			b.onMissing(locale, key)
		}

		return key
	}

	return interpolate(m.pick(locale, params["count"]), params)
}

// Lookup returns the raw template of key for locale (with fallback), or
// false. A plural message returns its "other" form.
func (b *Bundle) Lookup(locale, key string) (string, bool) {
	m, ok := b.find(locale, key)
	if !ok {
		return "", false
	}

	if m.forms != nil {
		return m.forms[Other], true
	}

	return m.text, true
}

// Messages returns the catalog a client needs to translate for locale:
// every key resolved along the same fallback chain as T, as raw templates
// (strings, or map[string]string of plural forms). With prefixes, only keys
// starting with one of them are returned, which keeps server-only messages
// out of the page. The result is a fresh map the caller may modify.
func (b *Bundle) Messages(locale string, prefixes ...string) map[string]any {
	b.mu.RLock()
	defer b.mu.RUnlock()

	chain := b.chain(locale)
	out := map[string]any{}

	// Least specific first, so more specific locales overwrite.
	for _, l := range slices.Backward(chain) {
		for key, m := range b.catalogs[l] {
			if len(prefixes) > 0 && !slices.ContainsFunc(prefixes, func(p string) bool {
				return strings.HasPrefix(key, p)
			}) {
				continue
			}

			if m.forms != nil {
				out[key] = maps.Clone(m.forms)
			} else {
				out[key] = m.text
			}
		}
	}

	return out
}

// LocalizeIssues rebuilds the built-in validation messages of is from the
// "validation.*" keys of locale (see z.Issues.Localize); untranslated
// issues keep their English message.
func (b *Bundle) LocalizeIssues(locale string, is z.Issues) z.Issues {
	return is.Localize(func(key string) (string, bool) {
		return b.Lookup(locale, ValidationPrefix+key)
	})
}

func (b *Bundle) find(locale, key string) (message, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, l := range b.chain(locale) {
		if m, ok := b.catalogs[l][key]; ok {
			return m, true
		}
	}

	return message{}, false
}

// chain is the lookup order for locale: its truncations, then the
// default's. Invalid locales use the default chain only.
func (b *Bundle) chain(locale string) []string {
	var out []string
	if canon, ok := Canonical(locale); ok {
		out = truncations(canon)
	}

	for _, l := range truncations(b.def) {
		if !slices.Contains(out, l) {
			out = append(out, l)
		}
	}

	return out
}

func (m message) pick(locale string, count any) string {
	if m.forms == nil {
		return m.text
	}

	n, ok := toFloat(count)
	if !ok {
		return m.forms[Other]
	}

	if s, ok := m.forms[Zero]; ok && n == 0 {
		return s
	}

	if s, ok := m.forms[PluralCategory(locale, n)]; ok {
		return s
	}

	return m.forms[Other]
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func argMap(args []any) map[string]any {
	if len(args) == 1 {
		if m, ok := args[0].(map[string]any); ok {
			return m
		}
	}

	out := make(map[string]any, len(args)/2)

	for i := 0; i+1 < len(args); i += 2 {
		if k, ok := args[i].(string); ok {
			out[k] = args[i+1]
		}
	}

	return out
}

func interpolate(tmpl string, params map[string]any) string {
	if len(params) == 0 || !strings.Contains(tmpl, "{") {
		return tmpl
	}

	return placeholder.ReplaceAllStringFunc(tmpl, func(m string) string {
		if v, ok := params[m[1:len(m)-1]]; ok {
			return fmt.Sprint(v)
		}

		return m
	})
}
