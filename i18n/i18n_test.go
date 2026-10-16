package i18n

import (
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
)

var langFS = fstest.MapFS{
	"en.json": {Data: []byte(`{
		"welcome": "Hello, {name}!",
		"posts": {
			"created": "Post created.",
			"count": {"zero": "No posts", "one": "{count} post", "other": "{count} posts"}
		},
		"flat.key": "Flat key",
		"only.en": "English only"
	}`)},
	"pt.json": {
		Data: []byte(
			"{\r\n\"welcome\": \"Olá, {name}!\",\r\n\"posts\": {\"count\": {\"one\": \"{count} post\", \"other\": \"{count} posts\"}}\r\n}",
		),
	},
	"pt_BR.json": {Data: []byte(`{"posts.created": "Post criado."}`)},
	"ru.json": {Data: []byte(`{"files": {"one": "{count} файл", "few": "{count} файла", "many": "{count} файлов", ` +
		`"other": "{count} файла"}}`)},
	"README.md": {Data: []byte("ignored")},
}

func loadTest(t *testing.T, opts Options) *Bundle {
	t.Helper()

	b, err := Load(langFS, opts)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	return b
}

func TestLoad_Locales(t *testing.T) {
	t.Parallel()

	b := loadTest(t, Options{})

	if diff := cmp.Diff([]string{"en", "pt", "pt-BR", "ru"}, b.Locales()); diff != "" {
		t.Errorf("Locales() (-want +got):\n%s", diff)
	}

	if got := b.Default(); got != "en" {
		t.Errorf("Default() = %q, want %q", got, "en")
	}
}

func TestLoad_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fsys fstest.MapFS
		opts Options
		want string
	}{
		{"bad json", fstest.MapFS{"en.json": {Data: []byte(`{`)}}, Options{}, "en.json"},
		{
			"bad locale file name",
			fstest.MapFS{"e n.json": {Data: []byte(`{}`)}},
			Options{},
			"invalid locale",
		},
		{
			"duplicate locale",
			fstest.MapFS{"pt-BR.json": {Data: []byte(`{}`)}, "pt_br.json": {Data: []byte(`{}`)}},
			Options{},
			"duplicate",
		},
		{"unsupported value", fstest.MapFS{"en.json": {Data: []byte(`{"a": 1}`)}}, Options{}, `"a"`},
		{"array value", fstest.MapFS{"en.json": {Data: []byte(`{"a": ["x"]}`)}}, Options{}, `"a"`},
		{"invalid default", fstest.MapFS{}, Options{Default: "english!"}, "invalid locale"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Load(tt.fsys, tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestBundle_T(t *testing.T) {
	t.Parallel()

	b := loadTest(t, Options{})

	tests := []struct {
		name   string
		locale string
		key    string
		args   []any
		want   string
	}{
		{"interpolation pairs", "en", "welcome", []any{"name", "Ana"}, "Hello, Ana!"},
		{"interpolation map", "pt", "welcome", []any{map[string]any{"name": "Ana"}}, "Olá, Ana!"},
		{"missing placeholder kept", "en", "welcome", nil, "Hello, {name}!"},
		{"nested key", "en", "posts.created", nil, "Post created."},
		{"flat key with dots", "en", "flat.key", nil, "Flat key"},
		{"region file", "pt-BR", "posts.created", nil, "Post criado."},
		{"region falls back to base", "pt-BR", "welcome", []any{"name", "Bia"}, "Olá, Bia!"},
		{"falls back to default", "pt-BR", "only.en", nil, "English only"},
		{"unknown locale uses default", "de", "posts.created", nil, "Post created."},
		{"missing key returns key", "en", "nope.missing", nil, "nope.missing"},
		{"namespace is not a message", "en", "posts", nil, "posts"},
		{"plural one", "en", "posts.count", []any{"count", 1}, "1 post"},
		{"plural other", "en", "posts.count", []any{"count", 5}, "5 posts"},
		{"plural explicit zero", "en", "posts.count", []any{"count", 0}, "No posts"},
		{"plural float", "en", "posts.count", []any{"count", 1.5}, "1.5 posts"},
		{"plural without count is other", "en", "posts.count", nil, "{count} posts"},
		{"plural non-numeric count is other", "en", "posts.count", []any{"count", "x"}, "x posts"},
		{"plural pt zero is one", "pt", "posts.count", []any{"count", 0}, "0 post"},
		{"plural ru one", "ru", "files", []any{"count", 21}, "21 файл"},
		{"plural ru few", "ru", "files", []any{"count", 3}, "3 файла"},
		{"plural ru many", "ru", "files", []any{"count", 11}, "11 файлов"},
		{"plural int64", "en", "posts.count", []any{"count", int64(1)}, "1 post"},
		{"odd args ignored", "en", "welcome", []any{"name", "Ana", "extra"}, "Hello, Ana!"},
		{"non-string arg key ignored", "en", "welcome", []any{1, "x"}, "Hello, {name}!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := b.T(tt.locale, tt.key, tt.args...); got != tt.want {
				t.Errorf("T(%q, %q, %v) = %q, want %q", tt.locale, tt.key, tt.args, got, tt.want)
			}
		})
	}
}

func TestBundle_OnMissing(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		got []string
	)

	b := loadTest(t, Options{OnMissing: func(locale, key string) {
		mu.Lock()
		defer mu.Unlock()

		got = append(got, locale+":"+key)
	}})

	b.T("pt-BR", "nope")
	b.T("pt-BR", "welcome")

	if diff := cmp.Diff([]string{"pt-BR:nope"}, got); diff != "" {
		t.Errorf("OnMissing calls (-want +got):\n%s", diff)
	}
}

func TestBundle_AddAndHas(t *testing.T) {
	t.Parallel()

	b, err := New(Options{Default: "pt-br"})
	if err != nil {
		t.Fatal(err)
	}

	if got := b.Default(); got != "pt-BR" {
		t.Errorf("Default() = %q, want %q", got, "pt-BR")
	}

	if err := b.Add(
		"es",
		map[string]any{"hi": "Hola", "n": map[string]string{"one": "uno", "other": "otros"}},
	); err != nil {
		t.Fatal(err)
	}

	if err := b.Add("es", map[string]any{"bye": "Adiós"}); err != nil {
		t.Fatal(err)
	}

	if err := b.Add("bad locale", map[string]any{}); err == nil {
		t.Error("Add(invalid locale) error = nil, want error")
	}

	if !b.Has("es", "hi") || !b.Has("es", "bye") || b.Has("es", "nope") {
		t.Error("Has() does not reflect merged catalogs")
	}

	if got := b.T("es", "n", "count", 2); got != "otros" {
		t.Errorf("T(es, n, 2) = %q, want %q", got, "otros")
	}
}

func TestBundle_Match(t *testing.T) {
	t.Parallel()

	b := loadTest(t, Options{})

	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"pt-BR", "pt-BR", true},
		{"pt_br", "pt-BR", true},
		{"pt-PT", "pt", true},
		{"PT", "pt", true},
		{"en-GB", "en", true},
		{"ru-RU", "ru", true},
		{"de", "", false},
		{"", "", false},
		{"../../etc/passwd", "", false},
	}

	for _, tt := range tests {
		got, ok := b.Match(tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("Match(%q) = %q, %v, want %q, %v", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}

	only := &Bundle{def: "en", catalogs: map[string]map[string]message{"pt-BR": {}}}
	if got, ok := only.Match("pt"); got != "pt-BR" || !ok {
		t.Errorf("Match(pt) with only pt-BR = %q, %v, want pt-BR, true", got, ok)
	}
}

func TestCanonical(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"en", "en", true},
		{"EN-us", "en-US", true},
		{"pt_br", "pt-BR", true},
		{"zh-hant-tw", "zh-Hant-TW", true},
		{"es-419", "es-419", true},
		{"fil", "fil", true},
		{"", "", false},
		{"e", "", false},
		{"english", "", false},
		{"en-", "", false},
		{"en-US-x", "", false},
		{"en/US", "", false},
		{"../en", "", false},
		{"en\x00", "", false},
		{"en-ÜS", "", false},
		{strings.Repeat("a", 100), "", false},
	}

	for _, tt := range tests {
		got, ok := Canonical(tt.in)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("Canonical(%q) = %q, %v, want %q, %v", tt.in, got, ok, tt.want, tt.wantOK)
		}
	}
}
