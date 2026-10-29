package i18n

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type pathResult struct {
	Path, RawPath, Prefix, Locale, Localized string
}

// VF-024 (HexaScore): a locale chosen by the first path segment, stripped
// before routing, and re-added to generated URLs.
func TestStripPathPrefix(t *testing.T) {
	t.Parallel()

	b := loadTest(t, Options{})
	cfg := MiddlewareConfig{PathPrefixes: map[string]string{"pt-br": "pt-BR"}}

	tests := []struct {
		target string
		accept string
		want   pathResult
	}{
		{"/pt-br/games/zelda?x=1", "", pathResult{"/games/zelda", "", "/pt-br", "pt-BR", "/pt-br/games"}},
		{"/pt-br", "", pathResult{"/", "", "/pt-br", "pt-BR", "/pt-br/games"}},
		{"/pt-br/", "", pathResult{"/", "", "/pt-br", "pt-BR", "/pt-br/games"}},
		{"/pt-br/a%2Fb", "", pathResult{"/a/b", "/a%2Fb", "/pt-br", "pt-BR", "/pt-br/games"}},
		// Not a whole segment, another case, or no prefix: left alone, and
		// the next sources (here the default) decide.
		{"/pt-brx/games", "", pathResult{"/pt-brx/games", "", "", "en", "/games"}},
		{"/PT-BR/games", "", pathResult{"/PT-BR/games", "", "", "en", "/games"}},
		{"/games", "pt-BR", pathResult{"/games", "", "", "pt-BR", "/games"}},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			t.Parallel()

			var got pathResult

			h := StripPathPrefix(cfg.PathPrefixes)(mustMiddleware(t, b, cfg)(
				http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					got = pathResult{
						Path: r.URL.Path, RawPath: r.URL.RawPath, Prefix: PathPrefix(r.Context()),
						Locale: Locale(r.Context()), Localized: LocalizePath(r.Context(), "/games"),
					}
				})))

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.target, http.NoBody)
			if tt.accept != "" {
				r.Header.Set("Accept-Language", tt.accept)
			}

			h.ServeHTTP(httptest.NewRecorder(), r)

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("GET %s (-want +got):\n%s", tt.target, diff)
			}
		})
	}
}

func TestLocalizePathRoot(t *testing.T) {
	t.Parallel()

	var got string

	h := StripPathPrefix(map[string]string{"es": "es"})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = LocalizePath(r.Context(), "/")
	}))
	h.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/es/x", http.NoBody),
	)

	if got != "/es" {
		t.Errorf(`LocalizePath(ctx, "/") = %q, want "/es"`, got)
	}

	if got := LocalizePath(t.Context(), "/x"); got != "/x" {
		t.Errorf(`LocalizePath(no prefix, "/x") = %q, want "/x"`, got)
	}
}

func TestMiddlewareConfig_ValidatePathPrefixes(t *testing.T) {
	t.Parallel()

	b := loadTest(t, Options{})

	tests := []struct {
		name    string
		cfg     MiddlewareConfig
		wantErr string
	}{
		{"valid", MiddlewareConfig{PathPrefixes: map[string]string{"pt-br": "pt-BR"}}, ""},
		{"path source without prefixes", MiddlewareConfig{Order: []Source{SourcePath}}, "PathPrefixes"},
		{"prefixes without path source", MiddlewareConfig{
			Order: []Source{SourceQuery}, PathPrefixes: map[string]string{"pt-br": "pt-BR"},
		}, "SourcePath"},
		{"segment with a slash", MiddlewareConfig{PathPrefixes: map[string]string{"pt/br": "pt-BR"}}, "segment"},
		{"empty segment", MiddlewareConfig{PathPrefixes: map[string]string{"": "pt-BR"}}, "segment"},
		{"locale without catalog", MiddlewareConfig{PathPrefixes: map[string]string{"de": "de"}}, "catalog"},
		{"locale outside Locales", MiddlewareConfig{
			Locales: []string{"en"}, PathPrefixes: map[string]string{"pt-br": "pt-BR"},
		}, "Locales"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate(b)
			if tt.wantErr == "" && err != nil ||
				tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// FuzzStripPathPrefix: stripping only ever removes one whole leading
// segment, and the result is still an absolute path.
func FuzzStripPathPrefix(f *testing.F) {
	for _, s := range []string{"/pt-br/x", "/pt-br", "/pt-br/", "/pt-brx", "/", "/x/pt-br", "//pt-br"} {
		f.Add(s)
	}

	strip := StripPathPrefix(map[string]string{"pt-br": "pt-BR"})

	f.Fuzz(func(t *testing.T, p string) {
		if !strings.HasPrefix(p, "/") {
			return
		}

		var got, prefix string

		h := strip(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got, prefix = r.URL.Path, PathPrefix(r.Context())
		}))

		r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		r.URL.Path = p
		h.ServeHTTP(httptest.NewRecorder(), r)

		switch {
		case prefix == "":
			if got != p {
				t.Fatalf("unprefixed %q rewritten to %q", p, got)
			}
		case !strings.HasPrefix(got, "/") || strings.TrimSuffix(prefix+got, "/") != strings.TrimSuffix(p, "/"):
			t.Fatalf("strip(%q) = prefix %q path %q", p, prefix, got)
		}
	})
}
