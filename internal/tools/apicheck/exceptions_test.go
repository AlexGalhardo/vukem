package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var testMods = []module{ //nolint:gochecknoglobals // read-only test fixture
	{Dir: ".", Path: "example.com/core"},
	{Dir: "drivers/x", Path: "example.com/core/drivers/x"},
}

const validEntry = `{
	"module": "example.com/core",
	"symbol": "example.com/core/auth/hash.DefaultArgon2",
	"change": "removed",
	"justification": "the default parameters are below the OWASP minimum",
	"advisory": "https://github.com/AlexGalhardo/vukem/security/advisories/GHSA-xxxx-xxxx-xxxx",
	"version": "v1.2.1"
}`

func TestParseExceptions(t *testing.T) {
	t.Parallel()

	withField := func(field, value string) string {
		var b strings.Builder

		for line := range strings.Lines(validEntry) {
			if strings.HasPrefix(strings.TrimSpace(line), `"`+field+`"`) {
				comma := ","
				if !strings.HasSuffix(strings.TrimSpace(line), ",") {
					comma = ""
				}

				line = "\t\"" + field + "\": " + value + comma + "\n"
			}

			b.WriteString(line)
		}

		return `{"exceptions": [` + b.String() + `]}`
	}

	tests := []struct {
		name    string
		in      string
		wantErr string
		wantN   int
	}{
		{name: "empty list", in: `{"exceptions": []}`},
		{name: "valid entry", in: `{"exceptions": [` + validEntry + `]}`, wantN: 1},
		{
			name:  "valid entry with CRLF",
			in:    strings.ReplaceAll(`{"exceptions": [`+validEntry+`]}`, "\n", "\r\n"),
			wantN: 1,
		},
		{name: "not JSON", in: `exceptions = []`, wantErr: "malformed"},
		{name: "unknown field", in: `{"exceptions": [], "allow_all": true}`, wantErr: "malformed"},
		{
			name:    "unknown entry field",
			in:      withField("version", `"v1.2.1", "expires": "2027-01-01"`),
			wantErr: "malformed",
		},
		{name: "trailing data", in: `{"exceptions": []} {"exceptions": []}`, wantErr: "trailing data"},
		{name: "stray closing brace", in: `{"exceptions": []}}`, wantErr: "malformed"},
		{name: "trailing whitespace", in: "{\"exceptions\": []}\r\n\n"},
		{name: "version of another major", in: withField("version", `"v2.0.1"`), wantErr: "is not a release of module"},
		{name: "missing advisory", in: withField("advisory", `""`), wantErr: "missing advisory"},
		{name: "blank justification", in: withField("justification", `"  "`), wantErr: "missing justification"},
		{name: "missing version", in: withField("version", `""`), wantErr: "missing version"},
		{name: "http advisory", in: withField("advisory", `"http://example.com/GHSA-1"`), wantErr: "not an https link"},
		{name: "advisory without host", in: withField("advisory", `"https:///GHSA-1"`), wantErr: "not an https link"},
		{name: "advisory not a link", in: withField("advisory", `"GHSA-xxxx"`), wantErr: "not an https link"},
		{name: "pre-release version", in: withField("version", `"v1.2.1-rc.1"`), wantErr: "not vMAJOR.MINOR.PATCH"},
		{name: "unknown module", in: withField("module", `"example.com/other"`), wantErr: "unknown module"},
		{
			name:    "symbol outside the module",
			in:      withField("symbol", `"example.com/core2/auth.Hash"`),
			wantErr: "symbol is not in module",
		},
		{
			name:    "duplicate entry",
			in:      `{"exceptions": [` + validEntry + `,` + validEntry + `]}`,
			wantErr: "duplicate entry",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseExceptions(strings.NewReader(tt.in), testMods)
			if tt.wantErr == "" {
				if err != nil || len(got) != tt.wantN {
					t.Fatalf("parseExceptions() = %d entries, %v, want %d entries, nil", len(got), err, tt.wantN)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("parseExceptions() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadExceptions(t *testing.T) {
	t.Parallel()

	t.Run("missing file means no exceptions", func(t *testing.T) {
		t.Parallel()

		got, err := loadExceptions(filepath.Join(t.TempDir(), "none.json"), testMods)
		if err != nil || got != nil {
			t.Errorf("loadExceptions(missing) = %v, %v, want nil, nil", got, err)
		}
	})

	t.Run("names the file on error", func(t *testing.T) {
		t.Parallel()

		name := filepath.Join(t.TempDir(), "apicheck-exceptions.json")
		if err := os.WriteFile(name, []byte(`{"exceptions": [{}]}`), 0o600); err != nil {
			t.Fatal(err)
		}

		_, err := loadExceptions(name, testMods)
		if err == nil || !strings.Contains(err.Error(), "apicheck-exceptions.json") {
			t.Errorf("loadExceptions(bad) = %v, want an error naming the file", err)
		}
	})

	t.Run("repository file is valid", func(t *testing.T) {
		t.Parallel()

		root := filepath.Join("..", "..", "..")

		mods, err := discoverModules(root)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := loadExceptions(filepath.Join(root, ".config", "apicheck-exceptions.json"), mods); err != nil {
			t.Errorf("loadExceptions(.config/apicheck-exceptions.json) = %v, want nil", err)
		}
	})
}

func TestGate(t *testing.T) {
	t.Parallel()

	removed := change{Package: "./auth/hash", Subject: "DefaultArgon2", Message: "removed", Incompatible: true}
	other := change{Package: "./orm", Subject: "Old", Message: "removed", Incompatible: true}
	coreAt := func(base string, changes ...change) result {
		return result{Module: testMods[0], Base: base, Changes: changes}
	}

	exc := exception{
		Module:        "example.com/core",
		Symbol:        "example.com/core/auth/hash.DefaultArgon2",
		Change:        "removed",
		Justification: "unsafe default",
		Advisory:      "https://github.com/AlexGalhardo/vukem/security/advisories/GHSA-xxxx-xxxx-xxxx",
		Version:       "v1.2.1",
	}

	tests := []struct {
		name       string
		results    []result
		exceptions []exception
		wantErr    []string
		wantOut    []string
	}{
		{
			name:    "no changes passes",
			results: []result{coreAt("v1.2.0")},
		},
		{
			name:    "incompatible change without exception fails",
			results: []result{coreAt("v1.2.0", removed)},
			wantErr: []string{"incompatible API changes"},
			wantOut: []string{"::error title=apidiff::example.com/core/auth/hash.DefaultArgon2: removed\n"},
		},
		{
			name:       "exception allows exactly its change",
			results:    []result{coreAt("v1.2.0", removed)},
			exceptions: []exception{exc},
			wantOut: []string{
				"example.com/core: 0 incompatible",
				"::warning title=apidiff::example.com/core/auth/hash.DefaultArgon2: removed (allowed",
			},
		},
		{
			name:       "exception does not cover other changes",
			results:    []result{coreAt("v1.2.0", removed, other)},
			exceptions: []exception{exc},
			wantErr:    []string{"incompatible API changes"},
			wantOut:    []string{"::error title=apidiff::example.com/core/orm.Old: removed\n"},
		},
		{
			name: "different message is not covered",
			results: []result{coreAt("v1.2.0", change{
				Package:      "./auth/hash",
				Subject:      "DefaultArgon2",
				Message:      "changed from int to string",
				Incompatible: true,
			})},
			exceptions: []exception{exc},
			wantErr:    []string{"incompatible API changes", "is unused"},
		},
		{
			name:       "unused exception fails",
			results:    []result{coreAt("v1.2.0")},
			exceptions: []exception{exc},
			wantErr:    []string{"exception 0 (example.com/core/auth/hash.DefaultArgon2: removed) is unused"},
		},
		{
			name:       "exception for a skipped module is unused",
			results:    []result{{Module: testMods[0], Skipped: "no stable v1 tag"}},
			exceptions: []exception{exc},
			wantErr:    []string{"is unused"},
		},
		{
			name:       "exception expires once the base tag ships its version",
			results:    []result{coreAt("v1.2.1", removed)},
			exceptions: []exception{exc},
			wantErr:    []string{"expired: base tag v1.2.1 already ships v1.2.1", "incompatible API changes"},
		},
		{
			name: "nested module base tag keeps its prefix",
			results: []result{{
				Module: testMods[1], Base: "drivers/x/v1.0.0",
				Changes: []change{{Package: ".", Subject: "Dial", Message: "removed", Incompatible: true}},
			}},
			exceptions: []exception{{
				Module: "example.com/core/drivers/x", Symbol: "example.com/core/drivers/x.Dial", Change: "removed",
				Justification: "j", Advisory: "https://example.com/1", Version: "v1.0.1",
			}},
		},
		{
			name:       "an exception covers one change only",
			results:    []result{coreAt("v1.2.0", removed, removed)},
			exceptions: []exception{exc},
			wantErr:    []string{"incompatible API changes"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			err := gate(&out, tt.results, tt.exceptions)
			if len(tt.wantErr) == 0 && err != nil {
				t.Fatalf("gate() = %v, want nil", err)
			}

			for _, want := range tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("gate() = %v, want an error containing %q", err, want)
				}
			}

			if slices.Contains(tt.wantErr, "incompatible API changes") != errors.Is(err, errIncompatible) {
				t.Errorf("errors.Is(gate(), errIncompatible) = %t, want %t", errors.Is(err, errIncompatible),
					slices.Contains(tt.wantErr, "incompatible API changes"))
			}

			for _, want := range tt.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("gate() output = %q, want it to contain %q", out.String(), want)
				}
			}
		})
	}
}

func TestMajorMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mod   string
		major int
		want  bool
	}{
		{"example.com/core", 1, true},
		{"example.com/core", 0, true},
		{"example.com/core", 2, false},
		{"example.com/core/v2", 2, true},
		{"example.com/core/v2", 1, false},
		{"example.com/core/drivers/v3", 3, true},
		{"example.com/core/vendor", 1, true},
	}
	for _, tt := range tests {
		if got := majorMatches(tt.mod, tt.major); got != tt.want {
			t.Errorf("majorMatches(%q, %d) = %t, want %t", tt.mod, tt.major, got, tt.want)
		}
	}
}

func TestExceptionMatches(t *testing.T) {
	t.Parallel()

	e := exception{Module: "example.com/core", Symbol: "example.com/core.New", Change: "removed"}

	got := []bool{
		e.matches("example.com/core", change{Package: ".", Subject: "New", Message: "removed"}),
		e.matches("example.com/core", change{Package: "./x", Subject: "New", Message: "removed"}),
		e.matches("example.com/core/drivers/x", change{Package: ".", Subject: "New", Message: "removed"}),
	}
	if diff := cmp.Diff([]bool{true, false, false}, got); diff != "" {
		t.Errorf("matches() mismatch (-want +got):\n%s", diff)
	}
}
