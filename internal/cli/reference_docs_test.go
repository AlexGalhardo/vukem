package cli

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	root "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/apidocs"
)

// referenceText joins the CLI reference pages of the docs site.
func referenceText(t *testing.T) string {
	t.Helper()

	files, err := filepath.Glob("../../site/content/docs/cli/reference*.mdx")
	if err != nil || len(files) == 0 {
		t.Fatalf("no CLI reference pages found: %v", err)
	}

	var sb strings.Builder

	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}

		sb.Write(raw)
		sb.WriteByte('\n')
	}

	return sb.String()
}

// registryFlags maps each command of the real registries to its flag names:
// the global commands of this package and the built-in app commands.
func registryFlags() map[string][]string {
	out := map[string][]string{}

	collect := func(name string, define func(*flag.FlagSet)) {
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		if define != nil {
			define(fs)
		}

		names := []string{}

		fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
		out[name] = names
	}

	for _, c := range commands() {
		var define func(*flag.FlagSet)
		if c.flags != nil {
			define = func(fs *flag.FlagSet) { c.flags(fs) }
		}

		collect(c.name, define)
	}

	app := new(root.App)

	for _, c := range append(app.Commands(), apidocs.Command()) {
		collect(c.Name(), c.Flags)
	}

	return out
}

// Every registered command and every flag must be in the CLI reference
// pages (site/content/docs/cli/reference*.mdx).
func TestCLIReferenceDocumentsEveryCommandAndFlag(t *testing.T) {
	text := referenceText(t)
	registry := registryFlags()

	if len(registry) < 50 {
		t.Fatalf("registry has %d commands, want the full set", len(registry))
	}

	for name, flags := range registry {
		re := regexp.MustCompile("vukem " + regexp.QuoteMeta(name) + "([^A-Za-z0-9:_-]|$)")
		if !re.MatchString(text) {
			t.Errorf("command %q is not documented as `vukem %s` in the CLI reference", name, name)
		}

		for _, f := range flags {
			if !strings.Contains(text, "`-"+f+"`") && !strings.Contains(text, "`--"+f+"`") {
				t.Errorf("flag -%s of %q is not documented as `-%s` in the CLI reference", f, name, f)
			}
		}
	}
}
