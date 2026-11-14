package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func vukemVersion(t *testing.T, dir, ver string, args ...string) (int, string, string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code := Run(t.Context(), args, Env{Dir: dir, Stdout: &out, Stderr: &errOut, Now: fixedNow, Version: ver})

	return code, out.String(), errOut.String()
}

func TestNewPrebuiltTodo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	code, out, errOut := vukemVersion(t, dir, "v1.2.3", "new", "demo", "--prebuilt")
	if code != 0 {
		t.Fatalf("code = %d\n%s%s", code, out, errOut)
	}

	for _, want := range []string{"docker compose up", "vukem-example-todo:1.2.3"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	project := filepath.Join(dir, "demo")

	entries, err := os.ReadDir(project)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}

	if got := strings.Join(names, ","); got != ".env,README.md,compose.yaml" {
		t.Errorf("files = %s, want only .env, README.md and compose.yaml", got)
	}

	compose := read(t, project, "compose.yaml")
	if !strings.Contains(compose, "image: ghcr.io/alexgalhardo/vukem-example-todo:1.2.3\n") {
		t.Errorf("compose.yaml image:\n%s", compose)
	}

	if strings.Contains(compose, "postgres") || strings.Contains(compose, "mailpit") {
		t.Errorf("todo compose should run the app only:\n%s", compose)
	}

	if env := read(t, project, ".env"); !strings.Contains(env, "APP_KEY=base64:") {
		t.Errorf(".env = %q", env)
	}

	if !strings.Contains(read(t, project, "README.md"), "docker compose up") {
		t.Error("README lacks the run command")
	}
}

func TestNewPrebuiltFinance(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	if code, out, errOut := vukemVersion(t, dir, "v0.44.0", "new", "money", "--prebuilt", "--example", "finance"); code != 0 {
		t.Fatalf("code = %d\n%s%s", code, out, errOut)
	}

	project := filepath.Join(dir, "money")
	compose := read(t, project, "compose.yaml")

	for _, want := range []string{
		"image: ghcr.io/alexgalhardo/vukem-example-finance:0.44.0\n", "postgres:", "mailpit:", "SMTP_HOST: mailpit",
		"127.0.0.1:8025:8025",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose.yaml lacks %q", want)
		}
	}

	if env := read(t, project, ".env"); !strings.Contains(env, "APP_KEY=base64:") ||
		!strings.Contains(env, "ADMIN_EMAILS=admin@example.test") {
		t.Errorf(".env = %q", env)
	}

	if !strings.Contains(read(t, project, "README.md"), "db:seed FinanceAdminSeeder FinanceDemoSeeder") {
		t.Error("README lacks the seed command")
	}
}

// Images exist only for stable tags, so any other build runs latest.
func TestPrebuiltImageTag(t *testing.T) {
	t.Parallel()

	for ver, want := range map[string]string{
		"v0.44.0":                          "ghcr.io/alexgalhardo/vukem-example-todo:0.44.0",
		"devel":                            "ghcr.io/alexgalhardo/vukem-example-todo:latest",
		"v0.44.0-rc.1":                     "ghcr.io/alexgalhardo/vukem-example-todo:latest",
		"v0.44.1-0.20261101000000-abcdef0": "ghcr.io/alexgalhardo/vukem-example-todo:latest",
		"v0.44.0+dirty":                    "ghcr.io/alexgalhardo/vukem-example-todo:latest",
	} {
		if got := prebuiltImage("todo", ver); got != want {
			t.Errorf("prebuiltImage(%q) = %q, want %q", ver, got, want)
		}
	}
}

func TestNewPrebuiltUsage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, extra := range [][]string{
		{"--example", "none"}, {"--no-example"}, {"--minimal"}, {"--seed"}, {"--docker"}, {"--api"},
		{"--templ"}, {"--inertia", "react"}, {"--module", "x"}, {"--no-install"}, {"--standards"},
	} {
		args := append([]string{"new", "demo", "--prebuilt"}, extra...)

		code, _, errOut := vukem(t, dir, args...)
		if code != 2 || !strings.Contains(errOut, "--prebuilt") || !strings.Contains(errOut, extra[0]) {
			t.Errorf("%v = %d %q, want usage error naming --prebuilt and %s", extra, code, errOut, extra[0])
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "demo")); err == nil {
		t.Error("refused --prebuilt wrote the project directory")
	}
}

func TestNewPrebuiltPrompt(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		input string
		args  []string
		want  bool
	}{
		"docker":        {"2\n", nil, true},
		"source":        {"1\n", nil, false},
		"default":       {"\n", nil, false},
		"stack flag":    {"2\n", []string{"--api"}, false},
		"example none":  {"2\n", []string{"--example", "none"}, false},
		"example given": {"2\n", []string{"--example", "finance"}, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			o := newOpts{}
			kind := exampleTodo

			for i := 0; i < len(tc.args); i++ {
				switch tc.args[i] {
				case "--api":
					o.apiOnly = true
				case "--example":
					i++
					o.example, kind = tc.args[i], tc.args[i]
				}
			}

			env := &Env{Stdin: strings.NewReader(tc.input), Stdout: &out, Interactive: true}

			if got := askPrebuilt(env, o, kind); got != tc.want {
				t.Errorf("askPrebuilt = %v, want %v\n%s", got, tc.want, out.String())
			}

			asked := strings.Contains(out.String(), "Run ready-made in Docker")
			if wantAsked := !strings.Contains(strings.Join(tc.args, " "), "--api") && kind != exampleNone; asked != wantAsked {
				t.Errorf("asked = %v, want %v", asked, wantAsked)
			}
		})
	}

	var out bytes.Buffer
	if askPrebuilt(&Env{Stdin: strings.NewReader("2\n"), Stdout: &out}, newOpts{}, exampleTodo) || out.Len() != 0 {
		t.Error("without a terminal the default stays the full source and nothing is asked")
	}
}

var composeImage = regexp.MustCompile(`(?m)^\s*image:\s*(\S+)`)

// The prebuilt compose reuses the Postgres and Mailpit pins of the generated
// one; this keeps the two from drifting apart.
func TestPrebuiltComposeSharesImagePins(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile(filepath.Join("standards", "compose.yaml.tmpl"))
	if err != nil {
		t.Fatal(err)
	}

	for _, ref := range []string{postgresImage, mailpitImage} {
		if !strings.Contains(string(b), "image: "+ref) {
			t.Errorf("standards/compose.yaml.tmpl does not pin %s", ref)
		}
	}

	out, err := renderPrebuilt("compose.yaml.tmpl", prebuiltData{
		Name: "x", Example: exampleFinance, Image: prebuiltImage(exampleFinance, "v1.0.0"),
		PostgresImage: postgresImage, MailpitImage: mailpitImage, Finance: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, m := range composeImage.FindAllStringSubmatch(string(out), -1)[1:] {
		if !imageRef.MatchString(m[1]) {
			t.Errorf("image %q is not pinned by digest", m[1])
		}
	}

	for _, p := range composePorts(string(out)) {
		if !strings.HasPrefix(p, "127.0.0.1:") {
			t.Errorf("port %q is not bound to loopback", p)
		}
	}
}
