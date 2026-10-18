package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestAvailableLocales(t *testing.T) {
	t.Parallel()

	if diff := cmp.Diff([]string{"en", "pt-BR"}, availableLocales()); diff != "" {
		t.Errorf("availableLocales() diff (-want +got):\n%s", diff)
	}
}

func TestParseLocales(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		in      string
		want    []string
		wantErr bool
	}{
		"empty is the caller's default": {in: "", want: nil},
		"english only":                  {in: "en", want: []string{"en"}},
		"the fallback is always in":     {in: "pt-BR", want: []string{"en", "pt-BR"}},
		"duplicates collapse":           {in: " en , en ", want: []string{"en"}},
		"both":                          {in: "en,pt-BR", want: []string{"en", "pt-BR"}},
		"a locale with no catalog":      {in: "fr", wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := parseLocales(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseLocales(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}

			if tc.wantErr {
				return
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("parseLocales(%q) diff (-want +got):\n%s", tc.in, diff)
			}
		})
	}
}

// A generator writes the locales the project already has, so a second run
// never re-adds a catalog the developer deleted (A79). Without a project
// and without a request it writes English only.
func TestResolveLocales(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		onDisk     []string
		requested  []string
		want       string
		wantQuoted string
	}{
		"a new project gets english only":    {want: "en", wantQuoted: `"en"`},
		"the project's own locales win":      {onDisk: []string{"en", "pt-BR"}, want: "en,pt-BR", wantQuoted: `"en", "pt-BR"`},
		"a deleted locale stays deleted":     {onDisk: []string{"en"}, want: "en", wantQuoted: `"en"`},
		"an explicit request wins over disk": {onDisk: []string{"en"}, requested: []string{"en", "pt-BR"}, want: "en,pt-BR", wantQuoted: `"en", "pt-BR"`},
		"an empty lang folder falls back":    {onDisk: []string{}, want: "en", wantQuoted: `"en"`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := &Env{Dir: t.TempDir()}
			if tc.onDisk != nil {
				lang := filepath.Join(env.Dir, "app", "accounts", "lang")
				if err := os.MkdirAll(lang, 0o750); err != nil {
					t.Fatal(err)
				}

				for _, l := range tc.onDisk {
					if err := os.Mkdir(filepath.Join(lang, l), 0o750); err != nil {
						t.Fatal(err)
					}
				}
			}

			data := map[string]string{}
			if err := resolveLocales(env, tc.requested, data); err != nil {
				t.Fatal(err)
			}

			if got := data[localeKey]; got != tc.want {
				t.Errorf("resolveLocales() locales = %q, want %q", got, tc.want)
			}

			if got := data["Locales"]; got != tc.wantQuoted {
				t.Errorf("resolveLocales() Locales = %q, want %q", got, tc.wantQuoted)
			}
		})
	}
}

// Without a resolved locale set a scaffold that ships catalogs fails
// instead of writing locales nobody asked for (fail closed).
func TestInstallScaffoldRefusesCatalogsWithoutLocales(t *testing.T) {
	t.Parallel()

	env := &Env{Dir: t.TempDir(), Stdout: os.Stdout}

	err := installScaffold(env, "auth-core", map[string]string{"Module": "example.com/app"}, true)
	if err == nil {
		t.Fatal("installScaffold() without a locale set = nil, want an error")
	}
}

// A85: the generated API mints least-privilege keys, so the handover has
// to say so. A token issued the obvious way — no "abilities" field —
// carries nothing and is refused by GET /api/v1/me, which the generated
// TestAPITokenWithoutAbilityIsRefused asserts. Without a word about it,
// the developer's first 403 looks like a bug in the framework.
func TestInstallAPIHandoverExplainsAbilities(t *testing.T) {
	t.Parallel()

	msg := apiInstalled()

	for _, want := range []string{
		"abilities",         // names the concept
		"apiv1.Abilities()", // where the set lives
		"auth.Ability",      // how a route demands one
		"/docs/security/api-tokens",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the install:api handover does not mention %q:\n%s", want, msg)
		}
	}

	// Every ability the scaffold defines is named, so adding one to the
	// template without the handover fails here instead of silently
	// shipping a message that is missing it.
	tmpl, err := scaffoldFS.ReadFile("scaffolds/api/app/controllers/apiv1/abilities.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}

	abilities := regexp.MustCompile(`(?m)^\tAbility\w+ = "([^"]+)"$`).FindAllSubmatch(tmpl, -1)
	if len(abilities) == 0 {
		t.Fatal("no Ability constants found in the scaffold; the assertion below would prove nothing")
	}

	for _, m := range abilities {
		if !strings.Contains(msg, string(m[1])) {
			t.Errorf("the handover does not name the %q ability:\n%s", m[1], msg)
		}
	}

	// And it must not teach the wildcard the scaffold refuses.
	if strings.Contains(msg, `"*"`) {
		t.Errorf("the handover mentions the wildcard ability:\n%s", msg)
	}
}
