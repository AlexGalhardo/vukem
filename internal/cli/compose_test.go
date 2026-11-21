package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A published port without a host address binds on every interface, which
// exposes Postgres (default user and password) and Mailpit to the network.
// Every entry must be bound to loopback, in flow and block style.
func TestSecurity_A02_ComposeTemplateBindsLoopback(t *testing.T) {
	t.Parallel()

	for _, f := range []string{"standards/compose.yaml.tmpl", "../../.config/compose.yaml"} {
		t.Run(f, func(t *testing.T) {
			t.Parallel()

			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}

			ports := composePorts(string(b))
			if len(ports) == 0 {
				t.Fatalf("%s: no ports entries found, the check would pass vacuously", f)
			}

			for _, p := range ports {
				if !strings.HasPrefix(p, "127.0.0.1:") {
					t.Errorf("%s publishes %q on all interfaces, want 127.0.0.1:<host>:<container>", f, p)
				}
			}
		})
	}
}

// composePorts returns the unquoted entries under every ports: key, written
// either as ports: ["a", "b"] or as a block of "- a" lines.
func composePorts(src string) []string {
	var out []string
	inBlock := false

	for line := range strings.SplitSeq(src, "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "ports:"):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "ports:"))
			inBlock = rest == ""
			if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
				for item := range strings.SplitSeq(rest[1:len(rest)-1], ",") {
					if v := unquote(item); v != "" {
						out = append(out, v)
					}
				}
			}
		case inBlock && strings.HasPrefix(trimmed, "- "):
			if v := unquote(strings.TrimPrefix(trimmed, "- ")); v != "" {
				out = append(out, v)
			}
		default:
			inBlock = false
		}
	}

	return out
}

func unquote(s string) string {
	return strings.Trim(strings.TrimSpace(s), `"'`)
}

func renderStandardsCompose(t *testing.T, database bool) string {
	t.Helper()

	kind := "htmx"
	if !database {
		kind = "minimal"
	}

	data := newStandardsData(
		starterData{
			Name:   "blog",
			Module: "github.com/acme/blog",
		},
		kind,
		standardsOpts{standards: true, docker: true},
		2026,
	)

	out, err := renderStandardsFile("compose.yaml.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}

	return string(out)
}

// A project that starts on SQLite must come up with a plain `docker compose
// up`: the app may not wait for PostgreSQL, and the service is opt-in behind
// a profile (VF-091).
func TestStandardsComposeStartsWithoutPostgres(t *testing.T) {
	t.Parallel()

	for _, database := range []bool{true, false} {
		src := renderStandardsCompose(t, database)

		var active []string

		for line := range strings.SplitSeq(src, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "#") {
				active = append(active, line)
			}
		}

		code := strings.Join(active, "\n")

		if i := strings.Index(code, "depends_on:"); i >= 0 {
			block, _, _ := strings.Cut(code[i:], "\n\n")
			if strings.Contains(block, "postgres") {
				t.Errorf("database=%v: the app depends on postgres:\n%s", database, block)
			}
		}

		if database && !strings.Contains(code, "profiles: [postgres]") {
			t.Errorf("database=true: the postgres service is not behind a profile:\n%s", code)
		}

		if !database && strings.Contains(code, "postgres:") {
			t.Errorf("database=false: the compose file mentions postgres:\n%s", code)
		}
	}
}

// docker compose config is the authority on what `up` starts.
func TestStandardsComposeConfigServices(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("runs docker compose")
	}

	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker is not installed")
	}

	dir := t.TempDir()

	for name, content := range map[string]string{
		"compose.yaml": renderStandardsCompose(t, true), ".env": "APP_KEY=test\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.CommandContext(t.Context(), docker, "compose", "config", "--services")
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		t.Skipf("docker compose is not usable here: %v", err)
	}

	services := strings.Fields(string(out))
	slices.Sort(services)

	if want := []string{"app", "mailpit"}; !slices.Equal(services, want) {
		t.Errorf(
			"docker compose config --services = %v, want %v: postgres must stay behind its profile",
			services,
			want,
		)
	}
}

// git-cliff calls the GitHub API unless told --offline, and panics in an empty
// repository or without a network (VF-098), so the documented command and the
// template must stay local.
func TestStandardsCliffStaysOffline(t *testing.T) {
	t.Parallel()

	for _, f := range []string{"standards/cliff.toml", "standards/CHANGELOG.md.tmpl"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(string(b), "git cliff --offline") {
			t.Errorf("%s documents git cliff without --offline", f)
		}
	}

	cfg, err := os.ReadFile("standards/cliff.toml")
	if err != nil {
		t.Fatal(err)
	}

	for line := range strings.SplitSeq(string(cfg), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "[remote.") {
			t.Errorf("cliff.toml enables %q, which calls the GitHub API", line)
		}
	}
}
