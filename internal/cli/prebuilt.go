package cli

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"regexp"
	"strings"
	"text/template"

	"github.com/AlexGalhardo/vukem/config"
)

// prebuiltFS holds the compose.yaml and README of `vukem new --prebuilt`.
//
//go:embed prebuilt
var prebuiltFS embed.FS

// Same pins as standards/compose.yaml.tmpl; TestPrebuiltComposeSharesImagePins
// fails if they drift.
const (
	postgresImage = "postgres:18.6-alpine3.24@sha256:9ba53c7d41ea093baf5c1ba635ba5434c4f979880527d49bb7d9237d3193085b"
	mailpitImage  = "axllent/mailpit:v1.33.1@sha256:aef2aacc11f075ec0fb6874275ee4a421e688e11a96ebb296134d1735c7761ce"
)

// exampleImageRepo is the registry path of the example images; release.yml
// publishes <repo>-<example>.
const exampleImageRepo = "ghcr.io/alexgalhardo/vukem-example"

var releaseTagRE = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

type prebuiltData struct {
	Name, Example, Image        string
	PostgresImage, MailpitImage string
	Finance                     bool
}

// prebuiltImage names the example image matching this CLI. Only stable tags
// are published, so any other build (devel, pre-release, pseudo-version) runs
// latest.
func prebuiltImage(example, cliVersion string) string {
	tag := "latest"
	if releaseTagRE.MatchString(cliVersion) {
		tag = strings.TrimPrefix(cliVersion, "v")
	}

	return exampleImageRepo + "-" + example + ":" + tag
}

// prebuiltConflict returns the first flag of o that a prebuilt project cannot
// honor, or "". A prebuilt project has no source: stack, standards and
// seeding options have nothing to act on.
func prebuiltConflict(o newOpts) string {
	set := []struct {
		flag string
		on   bool
	}{
		{"--minimal", o.minimal},
		{"--no-example", o.noExample},
		{"--example none", o.example == exampleNone},
		{"--seed", o.seed},
		{"--api", o.apiOnly},
		{"--templ", o.templ},
		{"--inertia", o.inertiaFW != ""},
		{"--docker", o.std.docker},
		{"--module", o.module != ""},
		{"--framework", o.framework != ""},
		{"--pm", o.pmFlag != ""},
		{"--no-install", o.noInstall},
		{"--assets-image", o.assetsImage != ""},
		{"--standards", o.std.standards},
		{"--license", o.std.license != ""},
		{"--ci", o.std.ci},
		{"--deploy", o.std.deploy != ""},
	}

	for _, s := range set {
		if s.on {
			return s.flag
		}
	}

	return ""
}

// checkPrebuiltFlags refuses --prebuilt together with a flag it cannot honor.
func checkPrebuiltFlags(o newOpts) error {
	if !o.prebuilt {
		return nil
	}

	if c := prebuiltConflict(o); c != "" {
		return usagef("--prebuilt runs a ready-made image and writes no source: drop %s", c)
	}

	return nil
}

// runPrebuilt writes the prebuilt folder for kind, which must be an example.
func runPrebuilt(env *Env, name, kind string) error {
	if kind == exampleNone {
		return usagef("--prebuilt needs an example: choose todo or finance")
	}

	return writePrebuilt(env, name, kind)
}

// writePrebuilt writes the folder of `vukem new --prebuilt`: compose.yaml,
// .env with a fresh APP_KEY and README.md. It runs no container.
func writePrebuilt(env *Env, name, kind string) error {
	dir := env.path(name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}

	project := *env
	project.Dir = dir

	data := prebuiltData{
		Name: name, Example: kind, Image: prebuiltImage(kind, version(env)),
		PostgresImage: postgresImage, MailpitImage: mailpitImage, Finance: kind == exampleFinance,
	}

	for _, f := range []string{"compose.yaml", "README.md"} {
		out, err := renderPrebuilt(f+".tmpl", data)
		if err != nil {
			return err
		}

		if err := writeFile(project.path(f), out); err != nil {
			return err
		}
	}

	if data.Finance {
		// ADMIN_EMAILS must list the seed email, or the demo admin seeder refuses.
		const dotenv = "APP_ENV=development\nADMIN_EMAILS=admin@example.test\nFINANCE_ADMIN_EMAIL=admin@example.test\n"
		if err := writeFile(project.path(".env"), []byte(dotenv)); err != nil {
			return err
		}
	}

	if err := writeAppKey(&project, config.GenerateKey(), false); err != nil {
		return err
	}

	fmt.Fprintf(env.Stdout, "\nCreated %s (ready-made %s example, image %s). Next:\n\n"+
		"  1. cd %s\n  2. docker compose up\n  3. open http://localhost:3000\n\n"+
		"Nothing was built or started. README.md has the details.\n", name, kind, data.Image, name)

	return nil
}

func renderPrebuilt(file string, data prebuiltData) ([]byte, error) {
	src, err := prebuiltFS.ReadFile("prebuilt/" + file)
	if err != nil {
		return nil, err
	}

	t, err := template.New(file).Option("missingkey=error").Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("prebuilt %s: %w", file, err)
	}

	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("prebuilt %s: %w", file, err)
	}

	return b.Bytes(), nil
}

// askPrebuilt asks how to run an example when no flag decided it. Without a
// terminal, or with an option only a full source project uses, the answer is
// the full source.
func askPrebuilt(env *Env, o newOpts, kind string) bool {
	if kind == exampleNone || !env.Interactive || env.Stdin == nil {
		return false
	}

	o.example, o.noExample = "", false
	if prebuiltConflict(o) != "" {
		return false
	}

	return ask(env.Stdout, env.reader(), "How do you want to run it?", []option{
		{"source", "Develop locally (full source)"},
		{"docker", "Run ready-made in Docker (no source, just compose.yaml)"},
	}) == "docker"
}
